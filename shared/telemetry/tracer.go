// Package telemetry wires OpenTelemetry tracing for each service: it configures
// the tracer provider and exporter and provides HTTP/Gin middleware that starts
// a span and propagates trace context per request.
package telemetry

import (
	"context"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyredocs/shared/config"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// Tracer is the package-global tracer for creating spans, set by Init.
var Tracer trace.Tracer

// samplerFromEnv builds the trace sampler from OTEL_TRACES_SAMPLER_RATIO
// (0.0–1.0, default 1.0 = sample everything). It is ParentBased, so a sampling
// decision made at the edge is honored by every downstream service and NATS
// consumer — otherwise a trace would be sampled in one hop and dropped in the
// next, producing broken partial traces. Lower it (e.g. 0.1) once production
// RPS makes 100% sampling expensive in Tempo storage.
func samplerFromEnv() sdktrace.Sampler {
	ratio := 1.0
	if raw := strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER_RATIO")); raw != "" {
		if parsed, err := strconv.ParseFloat(raw, 64); err == nil && parsed >= 0 && parsed <= 1 {
			ratio = parsed
		} else {
			slog.Warn("invalid OTEL_TRACES_SAMPLER_RATIO, sampling everything", "value", raw)
		}
	}
	if ratio >= 1 {
		return sdktrace.AlwaysSample()
	}
	return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))
}

// probeRetryInterval is how often Init re-probes a collector that was not
// reachable at startup. A var so tests can shorten it.
var probeRetryInterval = 15 * time.Second

// Init sets up the OpenTelemetry tracer provider for the given service.
// It reads OTEL_EXPORTER_OTLP_ENDPOINT (default: http://localhost:4318) for the
// collector endpoint. Returns a shutdown function that should be deferred.
//
// When the collector is not reachable at startup, tracing is not given up for
// the life of the process: Init keeps probing every probeRetryInterval for up to
// OTEL_COLLECTOR_WAIT (default 10m) and installs the provider once the collector
// answers. Compose starts the services and the observability profile in
// parallel, so the collector is routinely a few seconds late; a one-shot probe
// left every service started before it with tracing off until a restart
// (measured 2026-11-13: services up at 15:14:50, collector at 15:15:13, no
// traces). Spans started before installation are dropped; Tracer is the
// global delegating tracer, so it switches to the real provider by itself.
func Init(serviceName string) func(context.Context) error {
	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if endpoint == "" {
		endpoint = "http://localhost:4318"
	}
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	Tracer = otel.Tracer(serviceName)

	var installed atomic.Pointer[sdktrace.TracerProvider]
	install := func() bool {
		tp, err := newProvider(serviceName, endpoint)
		if err != nil {
			slog.Warn("failed to create OTLP exporter, tracing disabled", "error", err)
			return false
		}
		installed.Store(tp)
		otel.SetTracerProvider(tp)
		slog.Info("OpenTelemetry tracing initialized", "service", serviceName, "endpoint", endpoint)
		return true
	}

	stop := make(chan struct{})
	var stopOnce sync.Once
	if probeEndpoint(endpoint) {
		install()
	} else {
		wait := config.GetEnvDuration("OTEL_COLLECTOR_WAIT", 10*time.Minute)
		interval := probeRetryInterval // read once: the goroutine must not touch the package var
		slog.Warn("OTLP collector unreachable, will retry", "endpoint", endpoint, "for", wait.String())
		go func() {
			deadline := time.Now().Add(wait)
			t := time.NewTicker(interval)
			defer t.Stop()
			for time.Now().Before(deadline) {
				select {
				case <-stop:
					return
				case <-t.C:
					if probeEndpoint(endpoint) {
						install()
						return
					}
				}
			}
			slog.Warn("OTLP collector still unreachable, tracing disabled", "endpoint", endpoint, "waited", wait.String())
		}()
	}

	return func(ctx context.Context) error {
		stopOnce.Do(func() { close(stop) })
		if tp := installed.Load(); tp != nil {
			return tp.Shutdown(ctx)
		}
		return nil
	}
}

// newProvider builds a batching tracer provider exporting to endpoint.
func newProvider(serviceName, endpoint string) (*sdktrace.TracerProvider, error) {
	ctx := context.Background()
	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(tracesURL(endpoint)))
	if err != nil {
		return nil, err
	}
	res, err := resource.New(ctx, resource.WithAttributes(semconv.ServiceNameKey.String(serviceName)))
	if err != nil {
		slog.Warn("failed to create resource", "error", err)
		res = resource.Default()
	}
	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(samplerFromEnv()),
	), nil
}

// tracesURL turns OTEL_EXPORTER_OTLP_ENDPOINT, which the OTLP spec defines as a
// BASE URL (e.g. http://otel-collector:4318), into the traces URL by appending
// /v1/traces when the URL has no path. otlptracehttp.WithEndpointURL stopped
// appending it itself (v1.47.0: "the path of the provided URL is used as-is"),
// so after the 2026-11-09 dependency bump every service posted spans to "/" and
// the collector answered 404 — tracing silently stopped. A URL that already
// carries a path is passed through unchanged; an unparsable one is returned as
// is and the exporter keeps its default.
func tracesURL(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return endpoint
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/v1/traces"
	}
	return u.String()
}

// probeEndpoint performs a quick TCP dial to the collector host to check
// reachability. Returns false if the host cannot be reached within 2 seconds.
func probeEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		if u.Scheme == "https" {
			host += ":443"
		} else {
			host += ":80"
		}
	}
	conn, err := net.DialTimeout("tcp", host, 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

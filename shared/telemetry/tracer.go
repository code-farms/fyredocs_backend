package telemetry

import (
	"context"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

var Tracer trace.Tracer

// Init sets up the OpenTelemetry tracer provider for the given service.
// It reads OTEL_EXPORTER_OTLP_ENDPOINT (default: http://localhost:4318) for the collector endpoint.
// Returns a shutdown function that should be deferred.
func Init(serviceName string) func(context.Context) error {
	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if endpoint == "" {
		endpoint = "http://localhost:4318"
	}

	// Probe the collector endpoint before creating the exporter.
	// If the host is unreachable (e.g. missing docker service), disable
	// tracing instead of spamming logs with export timeouts.
	if !probeEndpoint(endpoint) {
		slog.Warn("OTLP collector unreachable, tracing disabled", "endpoint", endpoint)
		Tracer = otel.Tracer(serviceName)
		return func(context.Context) error { return nil }
	}

	ctx := context.Background()

	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(endpoint),
	)
	if err != nil {
		slog.Warn("failed to create OTLP exporter, tracing disabled", "error", err)
		Tracer = otel.Tracer(serviceName)
		return func(context.Context) error { return nil }
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
		),
	)
	if err != nil {
		slog.Warn("failed to create resource", "error", err)
		res = resource.Default()
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	Tracer = tp.Tracer(serviceName)
	slog.Info("OpenTelemetry tracing initialized", "service", serviceName, "endpoint", endpoint)

	return func(ctx context.Context) error {
		return tp.Shutdown(ctx)
	}
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

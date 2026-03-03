package telemetry

import (
	"context"
	"log/slog"
	"os"
	"strings"

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

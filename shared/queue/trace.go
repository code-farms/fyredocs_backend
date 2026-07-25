package queue

import (
	"context"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// W3C trace context propagation over NATS message headers (audit H1). HTTP
// hops already propagate traceparent via shared/telemetry middleware; these
// helpers extend the same propagator across the async NATS legs so one job is
// one trace: gateway → job-service → dispatch → worker → terminal event.

// natsHeaderCarrier adapts nats.Header (an http.Header clone) to the
// OpenTelemetry TextMapCarrier interface.
type natsHeaderCarrier nats.Header

func (c natsHeaderCarrier) Get(key string) string { return nats.Header(c).Get(key) }
func (c natsHeaderCarrier) Set(key, value string) { nats.Header(c).Set(key, value) }
func (c natsHeaderCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

// InjectTraceHeader returns a nats.Header carrying ctx's trace context
// (traceparent/tracestate/baggage, per the globally configured propagator).
func InjectTraceHeader(ctx context.Context) nats.Header {
	h := nats.Header{}
	otel.GetTextMapPropagator().Inject(ctx, natsHeaderCarrier(h))
	return h
}

// injectInto merges ctx's trace context into an existing header (used when a
// message is re-published — DLQ, redrive — and must keep its original headers).
func injectInto(ctx context.Context, h nats.Header) nats.Header {
	if h == nil {
		h = nats.Header{}
	}
	otel.GetTextMapPropagator().Inject(ctx, natsHeaderCarrier(h))
	return h
}

// ExtractTraceContext returns ctx augmented with any trace context carried in
// h. Safe on nil/empty headers (returns ctx unchanged).
func ExtractTraceContext(ctx context.Context, h nats.Header) context.Context {
	if len(h) == 0 {
		return ctx
	}
	return otel.GetTextMapPropagator().Extract(ctx, natsHeaderCarrier(h))
}

// StartConsumerSpan extracts the trace context from a consumed message's
// headers and starts a SpanKindConsumer child span named "<subject> process"
// with messaging.* semconv attributes. The caller must End() the span.
func StartConsumerSpan(ctx context.Context, tracer trace.Tracer, subject string, h nats.Header, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	ctx = ExtractTraceContext(ctx, h)
	if tracer == nil {
		tracer = otel.Tracer("fyredocs/shared/queue")
	}
	spanAttrs := append([]attribute.KeyValue{
		attribute.String("messaging.system", "nats"),
		attribute.String("messaging.destination.name", subject),
		attribute.String("messaging.operation.name", "process"),
	}, attrs...)
	return tracer.Start(ctx, subject+" process",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(spanAttrs...))
}

// startProducerSpan opens a SpanKindProducer span around a publish. Kept
// internal — the publish helpers below call it so every publisher gets the
// producer edge for free.
func startProducerSpan(ctx context.Context, subject string) (context.Context, trace.Span) {
	return otel.Tracer("fyredocs/shared/queue").Start(ctx, subject+" publish",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "nats"),
			attribute.String("messaging.destination.name", subject),
			attribute.String("messaging.operation.name", "publish"),
		))
}

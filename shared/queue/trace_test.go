package queue

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// withTestTracing installs a real propagator + always-sampling provider so
// inject/extract round-trips produce non-zero IDs, restoring globals after.
func withTestTracing(t *testing.T) trace.Tracer {
	t.Helper()
	prevProp := otel.GetTextMapPropagator()
	prevTP := otel.GetTracerProvider()
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{}))
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTextMapPropagator(prevProp)
		otel.SetTracerProvider(prevTP)
		_ = tp.Shutdown(context.Background())
	})
	return tp.Tracer("queue-test")
}

func TestInjectExtractRoundTrip(t *testing.T) {
	tracer := withTestTracing(t)

	ctx, parent := tracer.Start(context.Background(), "producer")
	defer parent.End()

	h := InjectTraceHeader(ctx)
	if h.Get("traceparent") == "" {
		t.Fatal("expected traceparent header to be injected")
	}

	got := ExtractTraceContext(context.Background(), h)
	gotSpanCtx := trace.SpanContextFromContext(got)
	wantSpanCtx := parent.SpanContext()
	if gotSpanCtx.TraceID() != wantSpanCtx.TraceID() {
		t.Errorf("trace ID lost in round trip: got %s, want %s", gotSpanCtx.TraceID(), wantSpanCtx.TraceID())
	}
	if gotSpanCtx.SpanID() != wantSpanCtx.SpanID() {
		t.Errorf("span ID lost in round trip: got %s, want %s", gotSpanCtx.SpanID(), wantSpanCtx.SpanID())
	}
}

func TestExtractTraceContextEmptyHeaderIsNoop(t *testing.T) {
	ctx := context.Background()
	if got := ExtractTraceContext(ctx, nil); got != ctx {
		t.Error("nil header must return the original context")
	}
}

func TestStartConsumerSpanIsChildOfProducer(t *testing.T) {
	tracer := withTestTracing(t)

	ctx, producer := tracer.Start(context.Background(), "publish")
	h := InjectTraceHeader(ctx)
	producer.End()

	_, span := StartConsumerSpan(context.Background(), tracer, "jobs.dispatch.optimize-pdf", h)
	defer span.End()

	if span.SpanContext().TraceID() != producer.SpanContext().TraceID() {
		t.Errorf("consumer span not in producer's trace: got %s, want %s",
			span.SpanContext().TraceID(), producer.SpanContext().TraceID())
	}
}

func TestCloneHeaderDoesNotAliasOriginal(t *testing.T) {
	tracer := withTestTracing(t)
	ctx, span := tracer.Start(context.Background(), "x")
	defer span.End()

	orig := InjectTraceHeader(ctx)
	origTP := orig.Get("traceparent")

	cloned := cloneHeader(orig)
	cloned.Set("traceparent", "changed")
	if orig.Get("traceparent") != origTP {
		t.Error("mutating the clone changed the original header")
	}
}

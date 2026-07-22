package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

// newTestLogger returns a logger writing JSON into buf, wired exactly like Init
// (contextHandler + replaceAttr) so tests exercise the real enrichment path.
func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	base := slog.NewJSONHandler(buf, &slog.HandlerOptions{ReplaceAttr: replaceAttr})
	return slog.New(&contextHandler{Handler: base})
}

func decodeLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("log line is not valid JSON: %v\n%s", err, buf.String())
	}
	return m
}

func TestContextHandlerInjectsTraceAndSpanID(t *testing.T) {
	tid, _ := trace.TraceIDFromHex("0123456789abcdef0123456789abcdef")
	sid, _ := trace.SpanIDFromHex("0123456789abcdef")
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	var buf bytes.Buffer
	newTestLogger(&buf).InfoContext(ctx, "hello")

	m := decodeLine(t, &buf)
	if m["trace_id"] != tid.String() {
		t.Errorf("trace_id = %v, want %s", m["trace_id"], tid.String())
	}
	if m["span_id"] != sid.String() {
		t.Errorf("span_id = %v, want %s", m["span_id"], sid.String())
	}
}

func TestContextHandlerInjectsRequestID(t *testing.T) {
	ctx := context.WithValue(context.Background(), requestIDKey{}, "req-123")

	var buf bytes.Buffer
	newTestLogger(&buf).InfoContext(ctx, "hello")

	if got := decodeLine(t, &buf)["request_id"]; got != "req-123" {
		t.Errorf("request_id = %v, want req-123", got)
	}
}

func TestContextHandlerNoCorrelationWithoutContext(t *testing.T) {
	var buf bytes.Buffer
	newTestLogger(&buf).InfoContext(context.Background(), "hello")

	m := decodeLine(t, &buf)
	for _, k := range []string{"trace_id", "span_id", "request_id"} {
		if _, ok := m[k]; ok {
			t.Errorf("unexpected %q on a context-less log: %v", k, m[k])
		}
	}
}

func TestReplaceAttrRedactsSensitiveKeys(t *testing.T) {
	var buf bytes.Buffer
	newTestLogger(&buf).InfoContext(context.Background(), "auth",
		"password", "hunter2", "token", "abc.def", "user", "bob")

	m := decodeLine(t, &buf)
	if m["password"] != "[REDACTED]" {
		t.Errorf("password = %v, want [REDACTED]", m["password"])
	}
	if m["token"] != "[REDACTED]" {
		t.Errorf("token = %v, want [REDACTED]", m["token"])
	}
	if m["user"] != "bob" {
		t.Errorf("user = %v, want bob (non-sensitive must pass through)", m["user"])
	}
}

func TestReplaceAttrForcesUTCTimestamp(t *testing.T) {
	var buf bytes.Buffer
	newTestLogger(&buf).InfoContext(context.Background(), "hello")

	ts, _ := decodeLine(t, &buf)["time"].(string)
	if ts == "" || !strings.HasSuffix(ts, "Z") {
		t.Errorf("time = %q, want a UTC RFC3339 timestamp ending in Z", ts)
	}
}

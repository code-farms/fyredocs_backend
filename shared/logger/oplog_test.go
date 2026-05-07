package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func captureLogs(t *testing.T, level slog.Level) (*bytes.Buffer, func()) {
	t.Helper()
	prev := slog.Default()
	buf := &bytes.Buffer{}
	h := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(h))
	return buf, func() { slog.SetDefault(prev) }
}

func decodeLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("invalid log JSON %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func TestLogErrEmitsStructuredError(t *testing.T) {
	buf, restore := captureLogs(t, slog.LevelDebug)
	defer restore()

	err := errors.New("boom")
	got := LogErr(context.Background(), "db.create_job", err, "jobId", "abc-123")

	if got != err {
		t.Fatalf("LogErr should return the same err, got %v", got)
	}
	logs := decodeLines(t, buf)
	if len(logs) != 1 {
		t.Fatalf("expected 1 log line, got %d: %s", len(logs), buf.String())
	}
	line := logs[0]
	if line["level"] != "ERROR" {
		t.Errorf("expected ERROR level, got %v", line["level"])
	}
	if line["msg"] != "db.create_job failed" {
		t.Errorf("unexpected msg: %v", line["msg"])
	}
	if line["op"] != "db.create_job" {
		t.Errorf("expected op=db.create_job, got %v", line["op"])
	}
	if line["err"] != "boom" {
		t.Errorf("expected err=boom, got %v", line["err"])
	}
	if line["jobId"] != "abc-123" {
		t.Errorf("expected jobId=abc-123, got %v", line["jobId"])
	}
}

func TestLogErrNilErrIsNoOp(t *testing.T) {
	buf, restore := captureLogs(t, slog.LevelDebug)
	defer restore()

	if got := LogErr(context.Background(), "noop", nil, "jobId", "abc"); got != nil {
		t.Fatalf("expected nil return, got %v", got)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no log output, got %s", buf.String())
	}
}

func TestLogErrIncludesRequestIDFromContext(t *testing.T) {
	buf, restore := captureLogs(t, slog.LevelDebug)
	defer restore()

	ctx := context.WithValue(context.Background(), requestIDKey{}, "req-xyz")
	LogErr(ctx, "redis.get", errors.New("nope"))

	logs := decodeLines(t, buf)
	if len(logs) != 1 {
		t.Fatalf("expected 1 log line, got %d", len(logs))
	}
	if logs[0]["requestId"] != "req-xyz" {
		t.Errorf("expected requestId=req-xyz, got %v", logs[0]["requestId"])
	}
}

func TestLogWarnEmitsAtWarnLevel(t *testing.T) {
	buf, restore := captureLogs(t, slog.LevelDebug)
	defer restore()

	err := errors.New("not found")
	got := LogWarn(context.Background(), "redis.lookup", err)
	if got != err {
		t.Fatalf("LogWarn should return the same err, got %v", got)
	}
	logs := decodeLines(t, buf)
	if len(logs) != 1 {
		t.Fatalf("expected 1 log line, got %d", len(logs))
	}
	if logs[0]["level"] != "WARN" {
		t.Errorf("expected WARN level, got %v", logs[0]["level"])
	}
	if logs[0]["op"] != "redis.lookup" {
		t.Errorf("expected op=redis.lookup, got %v", logs[0]["op"])
	}
}

func TestLogWarnNilErrIsNoOp(t *testing.T) {
	buf, restore := captureLogs(t, slog.LevelDebug)
	defer restore()

	if got := LogWarn(context.Background(), "noop", nil); got != nil {
		t.Fatalf("expected nil return, got %v", got)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no log output, got %s", buf.String())
	}
}

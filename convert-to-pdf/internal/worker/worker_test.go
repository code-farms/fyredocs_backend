package worker

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestBackoffDuration(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 10 * time.Second},
		{1, 30 * time.Second},
		{2, 2 * time.Minute},
		{3, 2 * time.Minute}, // capped at last element
		{10, 2 * time.Minute},
		{-1, 10 * time.Second}, // negative clamped to 0
		{-100, 10 * time.Second},
	}
	for _, tt := range tests {
		got := backoffDuration(tt.attempt)
		if got != tt.want {
			t.Errorf("backoffDuration(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}

func TestIsRecoverable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"status 500", errors.New("request failed: status=500"), true},
		{"status 502", errors.New("bad gateway: status=502"), true},
		{"status 429", errors.New("rate limited: status=429"), true},
		{"normal error", errors.New("file not found"), false},
		{"permission error", errors.New("permission denied"), false},
		{"timeout error", &timeoutErr{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRecoverable(tt.err)
			if got != tt.want {
				t.Errorf("isRecoverable(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

type timeoutErr struct{}

func (e *timeoutErr) Error() string   { return "timeout" }
func (e *timeoutErr) Timeout() bool   { return true }
func (e *timeoutErr) Temporary() bool { return true }

func TestFilepathBase(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"document.pdf", "document.pdf"},
		{"output.pdf", "output.pdf"},
	}
	for _, tt := range tests {
		got := filepathBase(tt.input)
		if got != tt.want {
			t.Errorf("filepathBase(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestOutputDir(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv("OUTPUT_DIR", "")
		got := outputDir()
		if got != "outputs" {
			t.Errorf("expected 'outputs', got %q", got)
		}
	})

	t.Run("custom", func(t *testing.T) {
		t.Setenv("OUTPUT_DIR", "/custom/path")
		got := outputDir()
		if got != "/custom/path" {
			t.Errorf("expected '/custom/path', got %q", got)
		}
	})
}

func TestJobPayloadUnmarshal(t *testing.T) {
	data := []byte(`{"eventType":"JobCreated","jobId":"abc-123","toolType":"word-to-pdf","attempts":1,"correlationId":"corr-456"}`)
	var payload JobPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.EventType != "JobCreated" {
		t.Errorf("expected EventType 'JobCreated', got %q", payload.EventType)
	}
	if payload.JobID != "abc-123" {
		t.Errorf("expected JobID 'abc-123', got %q", payload.JobID)
	}
	if payload.ToolType != "word-to-pdf" {
		t.Errorf("expected ToolType 'word-to-pdf', got %q", payload.ToolType)
	}
	if payload.CorrelationID != "corr-456" {
		t.Errorf("expected CorrelationID 'corr-456', got %q", payload.CorrelationID)
	}
}

func TestJobPayloadUnmarshalWithOptions(t *testing.T) {
	data := []byte(`{"eventType":"JobCreated","jobId":"abc","toolType":"split-pdf","options":{"range":"1-3"},"attempts":0,"correlationId":"c1"}`)
	var payload JobPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Options == nil {
		t.Fatal("expected non-nil options")
	}
	var opts map[string]interface{}
	if err := json.Unmarshal(payload.Options, &opts); err != nil {
		t.Fatal(err)
	}
	if opts["range"] != "1-3" {
		t.Errorf("expected range '1-3', got %v", opts["range"])
	}
}

func TestJobPayloadUnmarshalInvalid(t *testing.T) {
	data := []byte(`not valid json`)
	var payload JobPayload
	if err := json.Unmarshal(data, &payload); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

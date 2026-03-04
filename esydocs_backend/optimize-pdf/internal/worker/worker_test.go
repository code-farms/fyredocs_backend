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
		{3, 2 * time.Minute},
		{-1, 10 * time.Second},
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
		{"status 500", errors.New("status=500"), true},
		{"status 429", errors.New("status=429"), true},
		{"normal error", errors.New("file not found"), false},
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
	got := filepathBase("document.pdf")
	if got != "document.pdf" {
		t.Errorf("filepathBase('document.pdf') = %q", got)
	}
}

func TestOutputDir(t *testing.T) {
	t.Setenv("OUTPUT_DIR", "")
	if got := outputDir(); got != "outputs" {
		t.Errorf("expected 'outputs', got %q", got)
	}
	t.Setenv("OUTPUT_DIR", "/custom")
	if got := outputDir(); got != "/custom" {
		t.Errorf("expected '/custom', got %q", got)
	}
}

func TestJobPayloadUnmarshal(t *testing.T) {
	data := []byte(`{"eventType":"JobCreated","jobId":"abc","toolType":"compress-pdf","attempts":0,"correlationId":"c1"}`)
	var payload JobPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ToolType != "compress-pdf" {
		t.Errorf("expected 'compress-pdf', got %q", payload.ToolType)
	}
}

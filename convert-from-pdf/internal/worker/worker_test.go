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

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil error", nil, ""},
		{"timeout error", errors.New("context deadline exceeded"), ErrCodeTimeout},
		{"timeout keyword", errors.New("operation timeout"), ErrCodeTimeout},
		{"generic error", errors.New("file not found"), ErrCodeConversionFailed},
		{"libreoffice crash", errors.New("exit status 1"), ErrCodeConversionFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyError(tt.err)
			if got != tt.want {
				t.Errorf("classifyError(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestErrorCodeConstants(t *testing.T) {
	codes := map[string]string{
		"UNSUPPORTED_TOOL":  ErrCodeUnsupportedTool,
		"CONVERSION_FAILED": ErrCodeConversionFailed,
		"INVALID_PAYLOAD":   ErrCodeInvalidPayload,
		"OUTPUT_FAILED":     ErrCodeOutputFailed,
		"TIMEOUT":           ErrCodeTimeout,
	}
	for expected, got := range codes {
		if got != expected {
			t.Errorf("expected %q, got %q", expected, got)
		}
	}
}

func TestJobPayloadUnmarshal(t *testing.T) {
	data := []byte(`{"eventType":"JobCreated","jobId":"abc","toolType":"pdf-to-word","attempts":0,"correlationId":"c1"}`)
	var payload JobPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ToolType != "pdf-to-word" {
		t.Errorf("expected 'pdf-to-word', got %q", payload.ToolType)
	}
}

func TestConcurrencyFromEnv(t *testing.T) {
	t.Run("default is 2", func(t *testing.T) {
		t.Setenv("WORKER_CONCURRENCY", "")
		if got := concurrencyFromEnv(); got != 2 {
			t.Errorf("expected 2, got %d", got)
		}
	})

	t.Run("custom value", func(t *testing.T) {
		t.Setenv("WORKER_CONCURRENCY", "4")
		if got := concurrencyFromEnv(); got != 4 {
			t.Errorf("expected 4, got %d", got)
		}
	})

	t.Run("invalid uses default", func(t *testing.T) {
		t.Setenv("WORKER_CONCURRENCY", "abc")
		if got := concurrencyFromEnv(); got != 2 {
			t.Errorf("expected 2, got %d", got)
		}
	})

	t.Run("zero uses default", func(t *testing.T) {
		t.Setenv("WORKER_CONCURRENCY", "0")
		if got := concurrencyFromEnv(); got != 2 {
			t.Errorf("expected 2, got %d", got)
		}
	})

	t.Run("negative uses default", func(t *testing.T) {
		t.Setenv("WORKER_CONCURRENCY", "-1")
		if got := concurrencyFromEnv(); got != 2 {
			t.Errorf("expected 2, got %d", got)
		}
	})
}

package worker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

func TestHasRealProgress(t *testing.T) {
	tests := []struct {
		toolType string
		want     bool
	}{
		{"split-pdf", true},
		{"add-page-numbers", true},
		{"edit-pdf", true},
		{"word-to-pdf", false},
		{"excel-to-pdf", false},
		{"merge-pdf", false},
		{"compress-pdf", false},
		{"image-to-pdf", false},
		{"watermark-pdf", false},
	}
	for _, tt := range tests {
		t.Run(tt.toolType, func(t *testing.T) {
			got := hasRealProgress(tt.toolType)
			if got != tt.want {
				t.Errorf("hasRealProgress(%q) = %v, want %v", tt.toolType, got, tt.want)
			}
		})
	}
}

func TestIsOfficeConversion(t *testing.T) {
	tests := []struct {
		toolType string
		want     bool
	}{
		{"word-to-pdf", true},
		{"excel-to-pdf", true},
		{"ppt-to-pdf", true},
		{"html-to-pdf", true},
		{"image-to-pdf", false},
		{"merge-pdf", false},
		{"compress-pdf", false},
		{"split-pdf", false},
		{"watermark-pdf", false},
	}
	for _, tt := range tests {
		t.Run(tt.toolType, func(t *testing.T) {
			got := isOfficeConversion(tt.toolType)
			if got != tt.want {
				t.Errorf("isOfficeConversion(%q) = %v, want %v", tt.toolType, got, tt.want)
			}
		})
	}
}

func TestEstimateConversionTime(t *testing.T) {
	dir := t.TempDir()

	// Create a 1MB test file
	path1MB := filepath.Join(dir, "test1mb.docx")
	if err := os.WriteFile(path1MB, make([]byte, 1024*1024), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a 5MB test file
	path5MB := filepath.Join(dir, "test5mb.docx")
	if err := os.WriteFile(path5MB, make([]byte, 5*1024*1024), 0644); err != nil {
		t.Fatal(err)
	}

	t.Run("office 1MB", func(t *testing.T) {
		d := estimateConversionTime("word-to-pdf", []string{path1MB})
		// Expected: 2 + 1*1.5 = 3.5s
		if d < 3*time.Second || d > 4*time.Second {
			t.Errorf("expected ~3.5s, got %v", d)
		}
	})

	t.Run("office 5MB", func(t *testing.T) {
		d := estimateConversionTime("excel-to-pdf", []string{path5MB})
		// Expected: 2 + 5*1.5 = 9.5s
		if d < 9*time.Second || d > 10*time.Second {
			t.Errorf("expected ~9.5s, got %v", d)
		}
	})

	t.Run("pdfcpu 1MB", func(t *testing.T) {
		d := estimateConversionTime("merge-pdf", []string{path1MB})
		// Expected: 0.5 + 1*0.3 = 0.8s
		if d < 700*time.Millisecond || d > 900*time.Millisecond {
			t.Errorf("expected ~0.8s, got %v", d)
		}
	})

	t.Run("nonexistent file", func(t *testing.T) {
		d := estimateConversionTime("word-to-pdf", []string{"/nonexistent/file.docx"})
		// 0 bytes → base time only: 2s
		if d < 1900*time.Millisecond || d > 2100*time.Millisecond {
			t.Errorf("expected ~2s, got %v", d)
		}
	})

	t.Run("multiple files", func(t *testing.T) {
		d := estimateConversionTime("image-to-pdf", []string{path1MB, path1MB})
		// 2MB total, pdfcpu: 0.5 + 2*0.3 = 1.1s
		if d < 1*time.Second || d > 1200*time.Millisecond {
			t.Errorf("expected ~1.1s, got %v", d)
		}
	})
}

func TestProgressReporterStopsCleanly(t *testing.T) {
	// Verify that stop() doesn't hang and the done channel closes.
	pr := &progressReporter{done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	pr.cancel = cancel

	go func() {
		defer close(pr.done)
		<-ctx.Done()
	}()

	pr.stop()

	// If we get here, stop() returned successfully
	select {
	case <-pr.done:
		// expected
	default:
		t.Error("done channel should be closed after stop()")
	}
}

func TestConcurrencyFromEnv(t *testing.T) {
	t.Run("default is 2", func(t *testing.T) {
		t.Setenv("WORKER_CONCURRENCY", "")
		got := concurrencyFromEnv()
		if got != 2 {
			t.Errorf("expected 2, got %d", got)
		}
	})

	t.Run("custom value", func(t *testing.T) {
		t.Setenv("WORKER_CONCURRENCY", "4")
		got := concurrencyFromEnv()
		if got != 4 {
			t.Errorf("expected 4, got %d", got)
		}
	})

	t.Run("invalid uses default", func(t *testing.T) {
		t.Setenv("WORKER_CONCURRENCY", "abc")
		got := concurrencyFromEnv()
		if got != 2 {
			t.Errorf("expected 2, got %d", got)
		}
	})

	t.Run("zero uses default", func(t *testing.T) {
		t.Setenv("WORKER_CONCURRENCY", "0")
		got := concurrencyFromEnv()
		if got != 2 {
			t.Errorf("expected 2, got %d", got)
		}
	})

	t.Run("negative uses default", func(t *testing.T) {
		t.Setenv("WORKER_CONCURRENCY", "-1")
		got := concurrencyFromEnv()
		if got != 2 {
			t.Errorf("expected 2, got %d", got)
		}
	})
}

func TestErrorCodeConstants(t *testing.T) {
	if ErrCodeUnsupportedTool != "UNSUPPORTED_TOOL" {
		t.Errorf("ErrCodeUnsupportedTool = %q", ErrCodeUnsupportedTool)
	}
	if ErrCodeConversionFailed != "CONVERSION_FAILED" {
		t.Errorf("ErrCodeConversionFailed = %q", ErrCodeConversionFailed)
	}
	if ErrCodeInvalidPayload != "INVALID_PAYLOAD" {
		t.Errorf("ErrCodeInvalidPayload = %q", ErrCodeInvalidPayload)
	}
	if ErrCodeOutputFailed != "OUTPUT_FAILED" {
		t.Errorf("ErrCodeOutputFailed = %q", ErrCodeOutputFailed)
	}
	if ErrCodeTimeout != "TIMEOUT" {
		t.Errorf("ErrCodeTimeout = %q", ErrCodeTimeout)
	}
}

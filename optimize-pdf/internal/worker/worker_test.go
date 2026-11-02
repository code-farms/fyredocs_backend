package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
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

func TestFriendlyMessage(t *testing.T) {
	cases := map[string]string{
		ErrCodeTimeout:          "This file took too long to process. Please try again, or use a smaller file.",
		ErrCodeUnsupportedTool:  "This operation isn't supported for this file.",
		ErrCodeConversionFailed: "We couldn't process this file. It may be corrupted, password-protected, or in an unsupported format. Please try a different file.",
	}
	for code, want := range cases {
		if got := friendlyMessage(code); got != want {
			t.Errorf("friendlyMessage(%q) = %q, want %q", code, got, want)
		}
	}
	// Unknown code falls back to the generic message (never echoes the raw code).
	if got := friendlyMessage("SOMETHING_ELSE"); got != "Something went wrong while processing your file. Please try again." {
		t.Errorf("friendlyMessage(unknown) = %q, want generic default", got)
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

func TestStatusToEventType(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{"processing", "JobProgress"},
		{"completed", "JobCompleted"},
		{"failed", "JobFailed"},
		{"queued", "JobQueued"},
		{"unknown", "JobProgress"},
	}
	for _, tt := range tests {
		got := statusToEventType(tt.status)
		if got != tt.want {
			t.Errorf("statusToEventType(%q) = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestErrorCodeConstants(t *testing.T) {
	if ErrCodeUnsupportedTool != "UNSUPPORTED_TOOL" {
		t.Errorf("ErrCodeUnsupportedTool = %q", ErrCodeUnsupportedTool)
	}
	if ErrCodeConversionFailed != "CONVERSION_FAILED" {
		t.Errorf("ErrCodeConversionFailed = %q", ErrCodeConversionFailed)
	}
	if ErrCodeTimeout != "TIMEOUT" {
		t.Errorf("ErrCodeTimeout = %q", ErrCodeTimeout)
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

// ─── Backoff drift guard and error-classification rows ──────────────────────
//
// NOTE: duplicated across the four PDF workers by design (CLAUDE.md §1).

// TestBackoffTableMatchesConsumerConfig is the drift guard the comments on both
// sites ask for: the package-level backoff table (NakWithDelay timing) and the
// BackOff slice in Run's CreateOrUpdateConsumer call (ack-timeout redelivery
// timing) must stay equal, but nothing in code links them. This pin holds one
// side to the documented values; the compiler cannot reach the literal inside
// Run, so a change to either site must come through this test.
func TestBackoffTableMatchesConsumerConfig(t *testing.T) {
	// Mirrors the BackOff literal in Run's ConsumerConfig (worker.go).
	want := []time.Duration{10 * time.Second, 30 * time.Second, 2 * time.Minute}
	if len(backoff) != len(want) {
		t.Fatalf("backoff table has %d entries, want %d — the BackOff slice in Run must change in the same commit", len(backoff), len(want))
	}
	for i := range want {
		if backoff[i] != want[i] {
			t.Errorf("backoff[%d] = %v, want %v — the BackOff slice in Run must change in the same commit", i, backoff[i], want[i])
		}
	}
	// MaxDeliver semantics: handleFailure retries while deliveryCount < 4 (the
	// consumer's MaxDeliver), passing attempt = deliveryCount-1 to
	// backoffDuration. Deliveries 1..3 therefore consume attempts 0..2, so the
	// table needs exactly MaxDeliver-1 entries for the two retry paths to agree
	// without clamping.
	const maxDeliver = 4 // mirrors MaxDeliver in Run's ConsumerConfig
	if len(backoff) != maxDeliver-1 {
		t.Errorf("backoff table has %d entries but MaxDeliver %d implies %d retries", len(backoff), maxDeliver, maxDeliver-1)
	}
	for delivery := 1; delivery < maxDeliver; delivery++ {
		if got := backoffDuration(delivery - 1); got != backoff[delivery-1] {
			t.Errorf("delivery %d: backoffDuration = %v, want table entry %v", delivery, got, backoff[delivery-1])
		}
	}
}

// TestClassifyErrorContextSentinels pins how the real context sentinels
// classify (existing rows only cover hand-built errors.New strings):
// DeadlineExceeded's message contains "deadline exceeded" and maps to TIMEOUT;
// Canceled matches neither substring and falls through to CONVERSION_FAILED —
// a cancelled job reports as a generic conversion failure, pinned current
// behavior.
func TestClassifyErrorContextSentinels(t *testing.T) {
	if got := classifyError(context.DeadlineExceeded); got != ErrCodeTimeout {
		t.Errorf("classifyError(context.DeadlineExceeded) = %q, want %q", got, ErrCodeTimeout)
	}
	if got := classifyError(context.Canceled); got != ErrCodeConversionFailed {
		t.Errorf("classifyError(context.Canceled) = %q, want %q", got, ErrCodeConversionFailed)
	}
	// %w wrapping keeps the substring, so the classification survives wrapping.
	if got := classifyError(fmt.Errorf("process: %w", context.DeadlineExceeded)); got != ErrCodeTimeout {
		t.Errorf("classifyError(wrapped DeadlineExceeded) = %q, want %q", got, ErrCodeTimeout)
	}
}

// TestIsRecoverableContextAndWrapped pins the retry decision for the real
// context sentinels and for marked errors travelling through wrapping:
// DeadlineExceeded implements Timeout() and is retried; Canceled carries no
// transient signal and — per the permanent-by-default posture — is not.
func TestIsRecoverableContextAndWrapped(t *testing.T) {
	if !isRecoverable(context.DeadlineExceeded) {
		t.Error("context.DeadlineExceeded should be recoverable (implements Timeout() bool)")
	}
	if isRecoverable(context.Canceled) {
		t.Error("context.Canceled should not be recoverable (no transient signal)")
	}
	marked := markRecoverable(errors.New("connection reset"))
	if !isRecoverable(fmt.Errorf("stage inputs: %w", marked)) {
		t.Error("fmt.Errorf %w-wrapped marked error should stay recoverable")
	}
	if !isRecoverable(errors.Join(errors.New("cleanup failed"), marked)) {
		t.Error("errors.Join-ed marked error should stay recoverable")
	}
	// Unknown errors default to permanent — the safe posture for a worker.
	if isRecoverable(errors.New("exit status 1")) {
		t.Error("unknown error should default to permanent")
	}
}

// TestBuildStatusEventCarriesOwner pins that terminal events echo the job
// owner from the dispatch payload. document-service drops a JobCompleted with
// no UserID and analytics records it unattributed, so losing it is silent.
func TestBuildStatusEventCarriesOwner(t *testing.T) {
	jobID := uuid.New()
	queued := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

	ev := buildStatusEvent(jobID, "owner-1", "tool", "completed", 100, "", queued, 42)
	if ev.UserID != "owner-1" {
		t.Errorf("UserID = %q, want owner-1", ev.UserID)
	}
	if ev.EventType != "JobCompleted" || ev.JobID != jobID.String() || ev.FileSize != 42 || !ev.QueuedAt.Equal(queued) {
		t.Errorf("unexpected event: %+v", ev)
	}

	guest := buildStatusEvent(jobID, "", "tool", "failed", 0, "boom", queued)
	if guest.UserID != "" || guest.EventType != "JobFailed" || guest.FileSize != 0 {
		t.Errorf("unexpected guest event: %+v", guest)
	}
}

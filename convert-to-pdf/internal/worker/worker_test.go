package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/utils/tests"
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
		{"powerpoint-to-pdf", true},
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

// ─── Mid-job storage failure (resilience) ─────────────────────────────────────
//
// NOTE: this suite lives in convert-to-pdf ONLY. The four PDF workers duplicate
// worker.go and its tests by design (CLAUDE.md §1), but this test exercises the
// shared SHAPE of the pipeline — processMessage → storeOutput → handleFailure →
// NAK/ack discipline — with heavy scaffolding (fake jetstream.Msg, dry-run gorm
// handle, fake storage). That wiring is byte-identical across the workers, so
// the resilience pin is written once here rather than four times: duplicating
// the scaffolding would multiply maintenance surface without adding coverage.

// jsFakeMsg is a hand-rolled jetstream.Msg double (the repo's no-gomock
// convention, mirroring notification-service's fakeMsg) that records the
// disposition processMessage gives the message.
type jsFakeMsg struct {
	data     []byte
	meta     *jetstream.MsgMetadata
	acks     int
	naks     int
	terms    int
	nakDelay time.Duration
}

func (m *jsFakeMsg) Metadata() (*jetstream.MsgMetadata, error) { return m.meta, nil }
func (m *jsFakeMsg) Data() []byte                              { return m.data }
func (m *jsFakeMsg) Headers() nats.Header                      { return nil }
func (m *jsFakeMsg) Subject() string                           { return "jobs.dispatch.convert-to-pdf" }
func (m *jsFakeMsg) Reply() string                             { return "" }
func (m *jsFakeMsg) Ack() error                                { m.acks++; return nil }
func (m *jsFakeMsg) DoubleAck(context.Context) error           { m.acks++; return nil }
func (m *jsFakeMsg) Nak() error                                { m.naks++; return nil }
func (m *jsFakeMsg) NakWithDelay(d time.Duration) error        { m.naks++; m.nakDelay = d; return nil }
func (m *jsFakeMsg) InProgress() error                         { return nil }
func (m *jsFakeMsg) Term() error                               { m.terms++; return nil }
func (m *jsFakeMsg) TermWithReason(string) error               { m.terms++; return nil }

var _ jetstream.Msg = (*jsFakeMsg)(nil)

// dryRunWorkerDB returns a gorm handle that builds SQL but never executes it
// (DummyDialector + DryRun). The worker's status writes deliberately swallow DB
// errors (see the DB-helpers comment in worker.go), so the test's observable
// contract is the message disposition, not rows — the dry-run handle just keeps
// those swallow-error paths from panicking on a nil DB.
func dryRunWorkerDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(tests.DummyDialector{}, &gorm.Config{
		DryRun: true,
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open dry-run gorm: %v", err)
	}
	return db
}

// TestProcessMessageStoreOutputFailure pins the worker's behavior when the
// conversion SUCCEEDS but persisting the output to object storage fails
// mid-job. storeOutput failures are wrapped with markRecoverable (upload
// failures are network-ish), so handleFailure must NAK with the backoff delay
// for the current delivery while deliveries remain under MaxDeliver — the
// completed conversion work is retried, never silently discarded — and once
// deliveries reach MaxDeliver (4) the job is terminal: the message is ACKED
// (stop redelivery) and, with a live JetStream handle, a copy goes to the DLQ
// before the ack. cfg.JS is nil here, so the DLQ publish is skipped by its nil
// guard; the DLQ ordering itself is pinned by handleFailure's own comment and
// the job-service DLQ handler tests.
func TestProcessMessageStoreOutputFailure(t *testing.T) {
	jobID := uuid.New()
	payloadBytes, err := json.Marshal(JobPayload{
		EventType:     "JobCreated",
		JobID:         jobID.String(),
		ToolType:      "word-to-pdf",
		InputPaths:    []string{"users/u1/" + jobID.String() + "_input.docx"},
		CorrelationID: "corr-storeoutput",
	})
	if err != nil {
		t.Fatal(err)
	}

	// run drives one processMessage with an upload-erroring storage fake and a
	// ProcessFunc that genuinely produces an output file, returning the message
	// double for disposition assertions.
	run := func(t *testing.T, numDelivered uint64) (*jsFakeMsg, *bool) {
		t.Helper()
		store := newFakeStorage()
		store.uploadErr = errors.New("connection reset by peer")
		processed := false
		cfg := WorkerConfig{
			ServiceName:  "convert-to-pdf",
			AllowedTools: map[string]bool{"word-to-pdf": true},
			DB:           dryRunWorkerDB(t),
			Storage:      store,
			Process: func(_ context.Context, _ uuid.UUID, _ string, _ []string, _ map[string]interface{}, outputDir string, _ ProgressFunc) (*ProcessResult, error) {
				processed = true
				out := filepath.Join(outputDir, "result.pdf")
				if err := os.WriteFile(out, []byte("%PDF-1.7"), 0o644); err != nil {
					return nil, err
				}
				return &ProcessResult{OutputPath: out}, nil
			},
		}
		msg := &jsFakeMsg{data: payloadBytes, meta: &jetstream.MsgMetadata{NumDelivered: numDelivered}}
		processMessage(context.Background(), cfg, msg, testLogger())
		if !processed {
			t.Fatal("ProcessFunc never ran — the failure under test happens AFTER a successful conversion")
		}
		if len(store.uploads) != 0 {
			t.Fatalf("uploads recorded despite injected error: %v", store.uploads)
		}
		return msg, &processed
	}

	t.Run("first delivery is NAK'd with the first backoff delay", func(t *testing.T) {
		msg, _ := run(t, 1)
		if msg.naks != 1 {
			t.Fatalf("naks = %d, want 1 (recoverable failure must be retried)", msg.naks)
		}
		if msg.nakDelay != backoffDuration(0) {
			t.Errorf("nak delay = %v, want %v (backoff table entry for delivery 1)", msg.nakDelay, backoffDuration(0))
		}
		if msg.acks != 0 || msg.terms != 0 {
			t.Errorf("acks = %d, terms = %d, want 0/0 — an acked or termed message would never redeliver", msg.acks, msg.terms)
		}
	})

	t.Run("third delivery still retries with the last backoff entry", func(t *testing.T) {
		msg, _ := run(t, 3)
		if msg.naks != 1 {
			t.Fatalf("naks = %d, want 1 (deliveries under MaxDeliver keep retrying)", msg.naks)
		}
		if msg.nakDelay != backoffDuration(2) {
			t.Errorf("nak delay = %v, want %v", msg.nakDelay, backoffDuration(2))
		}
	})

	t.Run("delivery at MaxDeliver is terminal: acked, not NAK'd", func(t *testing.T) {
		msg, _ := run(t, 4)
		if msg.acks != 1 {
			t.Fatalf("acks = %d, want 1 (retries exhausted → ack to stop redelivery)", msg.acks)
		}
		if msg.naks != 0 || msg.terms != 0 {
			t.Errorf("naks = %d, terms = %d, want 0/0 on the terminal path", msg.naks, msg.terms)
		}
	})
}

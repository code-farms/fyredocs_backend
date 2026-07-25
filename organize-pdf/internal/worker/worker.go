// Package worker runs the NATS JetStream consumer loop shared by the PDF
// processing services. It pulls dispatched jobs, downloads inputs from object
// storage, invokes the service-specific Process function, uploads outputs, and
// publishes completion or failure events with bounded concurrency and retries.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"gorm.io/gorm"

	"organize-pdf/internal/models"

	"fyredocs/shared/queue"
	"fyredocs/shared/storage"
	"fyredocs/shared/telemetry"
)

// ProcessResult is what a Process function returns: the local path of the
// produced output and any metadata to attach to the completion event.
type ProcessResult struct {
	OutputPath string
	Metadata   map[string]interface{}
}

// ProcessFunc performs the actual work for a job. Each service supplies its own
// implementation; the worker handles transport, storage, and lifecycle.
type ProcessFunc func(ctx context.Context, jobID uuid.UUID, toolType string, inputPaths []string, options map[string]interface{}, outputDir string) (*ProcessResult, error)

// WorkerConfig wires the worker to its service: the tools it accepts, the
// Process implementation, and the NATS, DB, storage, and Redis dependencies.
type WorkerConfig struct {
	ServiceName  string
	AllowedTools map[string]bool
	Process      ProcessFunc
	JS           jetstream.JetStream
	DB           *gorm.DB
	Storage      Storage       // object storage for job inputs/outputs
	RedisClient  *redis.Client // optional – only used for processing markers; may be nil
}

// JobPayload is the dispatch message consumed from the JOBS_DISPATCH stream.
type JobPayload struct {
	EventType     string          `json:"eventType"`
	JobID         string          `json:"jobId"`
	ToolType      string          `json:"toolType"`
	InputPaths    []string        `json:"inputPaths,omitempty"`
	Options       json.RawMessage `json:"options,omitempty"`
	Attempts      int             `json:"attempts"`
	CorrelationID string          `json:"correlationId"`
	// QueuedAt is stamped by job-service at dispatch and echoed on every
	// status event so end-to-end job latency is observable (job_duration_seconds).
	QueuedAt time.Time `json:"queuedAt,omitempty"`
}

// Structured error codes for worker failures.
const (
	ErrCodeUnsupportedTool  = "UNSUPPORTED_TOOL"
	ErrCodeConversionFailed = "CONVERSION_FAILED"
	ErrCodeInvalidPayload   = "INVALID_PAYLOAD"
	ErrCodeOutputFailed     = "OUTPUT_FAILED"
	ErrCodeTimeout          = "TIMEOUT"
)

// classifyError returns a structured error code for a processing failure.
func classifyError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded") {
		return ErrCodeTimeout
	}
	return ErrCodeConversionFailed
}

// friendlyMessage maps a structured error code to a user-facing message. The raw
// error is logged separately (see handleFailure / the unsupported-tool branch),
// so users never see technical details while operators keep full context in logs.
func friendlyMessage(code string) string {
	switch code {
	case ErrCodeTimeout:
		return "This file took too long to process. Please try again, or use a smaller file."
	case ErrCodeUnsupportedTool:
		return "This operation isn't supported for this file."
	case ErrCodeConversionFailed:
		return "We couldn't process this file. It may be corrupted, password-protected, or in an unsupported format. Please try a different file."
	default:
		return "Something went wrong while processing your file. Please try again."
	}
}

// backoff mirrors the consumer BackOff configuration so that NakWithDelay
// can supply a sensible delay on each redelivery.
var backoff = []time.Duration{10 * time.Second, 30 * time.Second, 2 * time.Minute}

func backoffDuration(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt >= len(backoff) {
		return backoff[len(backoff)-1]
	}
	return backoff[attempt]
}

// drainInFlight waits for in-flight jobs up to WORKER_DRAIN_TIMEOUT; on
// timeout it cancels them via procCancel (they nak and get redelivered) and
// waits for the goroutines to unwind.
func drainInFlight(wg *sync.WaitGroup, procCancel context.CancelFunc, logger *slog.Logger) {
	if !waitDrain(wg, drainTimeout(), procCancel) {
		logger.Warn("drain timeout exceeded; cancelled in-flight jobs (they will be redelivered)")
	}
}

// waitDrain blocks until wg completes or timeout elapses. On timeout it calls
// onTimeout (used to cancel in-flight jobs) and then waits for wg to finish
// unwinding. Returns true when the drain completed within the deadline.
func waitDrain(wg *sync.WaitGroup, timeout time.Duration, onTimeout func()) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		if onTimeout != nil {
			onTimeout()
		}
		<-done
		return false
	}
}

// drainTimeout reads WORKER_DRAIN_TIMEOUT (default 120s). It must stay below
// the container's stop_grace_period (compose sets 3m) or Docker SIGKILLs the
// process before the drain finishes.
func drainTimeout() time.Duration {
	if v := os.Getenv("WORKER_DRAIN_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 120 * time.Second
}

// concurrencyFromEnv reads the WORKER_CONCURRENCY environment variable.
// Falls back to 2 if unset or invalid.
func concurrencyFromEnv() int {
	if v := os.Getenv("WORKER_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 2
}

// Run starts the worker loop and blocks until ctx is cancelled. It maintains a
// durable pull consumer, processes jobs up to a concurrency limit derived from
// the environment, and drains in-flight work on shutdown, bounded by
// WORKER_DRAIN_TIMEOUT (jobs still running at the deadline are cancelled and
// nak'd so JetStream redelivers them).
func Run(ctx context.Context, cfg WorkerConfig) {
	logger := slog.Default().With("service", cfg.ServiceName)

	maxConcurrency := concurrencyFromEnv()
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup

	// procCtx decouples job lifetime from the fetch loop: SIGTERM cancels ctx
	// (stop fetching immediately) while in-flight jobs keep running on procCtx
	// until the bounded drain below expires, at which point procCancel aborts
	// them (their failure path naks → redelivery). Without this split, cancel
	// would kill jobs mid-write the instant shutdown starts.
	procCtx, procCancel := context.WithCancel(context.WithoutCancel(ctx))
	defer procCancel()

	logger.Info("worker starting", "concurrency", maxConcurrency)

	// ── Create / get durable pull consumer on JOBS_DISPATCH stream ──
	cons, err := cfg.JS.CreateOrUpdateConsumer(ctx, "JOBS_DISPATCH", jetstream.ConsumerConfig{
		Durable:       cfg.ServiceName,
		FilterSubject: "jobs.dispatch." + cfg.ServiceName,
		AckPolicy:     jetstream.AckExplicitPolicy,
		MaxDeliver:    4,                  // 1 initial + 3 retries
		AckWait:       30 * time.Minute,   // must exceed the 10m per-job timeout or a running job gets redelivered and double-processed
		MaxAckPending: 2 * maxConcurrency, // bound unacked messages on a wedged container
		BackOff:       []time.Duration{10 * time.Second, 30 * time.Second, 2 * time.Minute},
	})
	if err != nil {
		logger.Error("failed to create NATS consumer", "error", err)
		return
	}

	for {
		select {
		case <-ctx.Done():
			logger.Info("worker shutting down, draining in-flight jobs")
			drainInFlight(&wg, procCancel, logger)
			return
		default:
		}

		// Pull up to maxConcurrency messages; jobs process in parallel goroutines
		// bounded by the semaphore so one slow job never blocks the rest.
		// The short FetchMaxWait keeps shutdown prompt — the loop re-checks
		// ctx.Done() at most 5s after SIGTERM instead of 30s.
		msgs, err := cons.Fetch(maxConcurrency, jetstream.FetchMaxWait(5*time.Second))
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				drainInFlight(&wg, procCancel, logger)
				return
			}
			logger.Error("fetch failed", "error", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}

		for msg := range msgs.Messages() {
			if ctx.Err() != nil {
				// Shutting down: never start new work on an already-fetched
				// message — nak it for prompt redelivery elsewhere.
				_ = msg.Nak()
				continue
			}
			sem <- struct{}{} // acquire semaphore slot
			wg.Add(1)
			go func(m jetstream.Msg) {
				defer wg.Done()
				defer func() { <-sem }() // release semaphore slot
				defer func() {
					if r := recover(); r != nil {
						logger.Error("worker panic recovered", "error", fmt.Sprintf("panic: %v", r), "op", "worker.process_panic")
						var p JobPayload
						if json.Unmarshal(m.Data(), &p) == nil && p.JobID != "" {
							const failMsg = "Processing failed unexpectedly. Please try again."
							updateJobStatusString(cfg.DB, p.JobID, "failed", 0, failMsg)
							if jid, e := uuid.Parse(p.JobID); e == nil {
								publishStatusEvent(queue.ExtractTraceContext(context.Background(), m.Headers()),
									cfg.JS, jid, p.ToolType, "failed", 0, failMsg, p.QueuedAt)
							}
						}
						_ = m.Term()
					}
				}()
				processMessage(procCtx, cfg, m, logger)
			}(msg)
		}

		if msgs.Error() != nil && !errors.Is(msgs.Error(), jetstream.ErrNoMessages) {
			logger.Error("message batch error", "error", msgs.Error())
		}
	}
}

func processMessage(ctx context.Context, cfg WorkerConfig, msg jetstream.Msg, logger *slog.Logger) {
	var payload JobPayload
	if err := json.Unmarshal(msg.Data(), &payload); err != nil {
		logger.Error("invalid payload", "error", err, "code", ErrCodeInvalidPayload)
		// Non-recoverable: ack to stop redelivery.
		_ = msg.Ack()
		return
	}

	// Continue the dispatcher's trace (W3C context rides in the NATS message
	// headers) so gateway → job-service → worker is one trace in Tempo. The
	// ctx also feeds the *Context slog calls below, which stamps trace_id on
	// worker logs (Grafana log→trace jump).
	ctx, span := queue.StartConsumerSpan(ctx, telemetry.Tracer, msg.Subject(), msg.Headers(),
		attribute.String("fyredocs.job_id", payload.JobID),
		attribute.String("fyredocs.tool_type", payload.ToolType),
		attribute.String("fyredocs.correlation_id", payload.CorrelationID),
	)
	defer span.End()

	if !cfg.AllowedTools[payload.ToolType] {
		logger.WarnContext(ctx, "unsupported tool", "tool", payload.ToolType, "jobId", payload.JobID)
		failMsg := friendlyMessage(ErrCodeUnsupportedTool)
		updateJobStatusString(cfg.DB, payload.JobID, "failed", 0, failMsg)
		if parsed, parseErr := uuid.Parse(payload.JobID); parseErr == nil {
			publishStatusEvent(ctx, cfg.JS, parsed, payload.ToolType, "failed", 0, failMsg, payload.QueuedAt)
		}
		// Non-recoverable: ack to stop redelivery.
		_ = msg.Ack()
		return
	}

	jobID, err := uuid.Parse(payload.JobID)
	if err != nil {
		logger.ErrorContext(ctx, "invalid job id", "jobId", payload.JobID, "error", err)
		_ = msg.Ack()
		return
	}

	// Skip if job is already completed or being processed by another worker
	var existingJob models.ProcessingJob
	if err := cfg.DB.Select("status").Where("id = ?", jobID).First(&existingJob).Error; err == nil {
		if existingJob.Status == "completed" || existingJob.Status == "processing" {
			logger.InfoContext(ctx, "job already "+existingJob.Status+", skipping duplicate", "jobId", payload.JobID)
			_ = msg.Ack()
			return
		}
	}

	updateJobStatus(cfg.DB, jobID, "processing", 20, "")
	publishStatusEvent(ctx, cfg.JS, jobID, payload.ToolType, "processing", 20, "", payload.QueuedAt)

	options := map[string]interface{}{}
	if len(payload.Options) > 0 && json.Valid(payload.Options) {
		if err := json.Unmarshal(payload.Options, &options); err != nil {
			logger.Error("failed to parse job options", "jobId", payload.JobID, "error", err)
		}
	}

	// Result-cache lookup: identical inputs+tool+options short-circuit the whole
	// download+convert pipeline. Best-effort — any failure falls through to a
	// normal conversion.
	cache := newCacheStore(cfg.RedisClient)
	ttl := cacheTTL()
	var cacheKey string
	cacheable := cache != nil && ttl > 0
	if cacheable {
		if etags, err := inputETags(ctx, cfg.Storage, payload.InputPaths); err == nil {
			cacheKey = buildCacheKey(payload.ToolType, payload.Options, etags)
			if tryServeFromCache(ctx, cfg, cache, cacheKey, payload, jobID, logger) {
				if err := msg.Ack(); err != nil {
					logger.Error("failed to ack cached message", "jobId", payload.JobID, "error", err)
				}
				return
			}
		} else {
			logger.Warn("cache: stat inputs failed, skipping cache", "jobId", payload.JobID, "error", err)
			cacheable = false
		}
	}

	// Stage inputs: download from the uploads bucket into a job-scoped
	// scratch directory (subprocess tools need real local files).
	scratch, err := os.MkdirTemp("", "job-"+payload.JobID+"-")
	if err != nil {
		handleFailure(ctx, cfg, msg, payload, markRecoverable(fmt.Errorf("create scratch dir: %w", err)), logger)
		return
	}
	defer os.RemoveAll(scratch)

	// Capacity guard: reject jobs whose projected scratch footprint would
	// exceed the worker's tmpfs budget, and serialize large jobs so two of them
	// never exhaust the shared scratch area concurrently.
	totalInput, err := totalInputSize(ctx, cfg.Storage, payload.InputPaths)
	if err != nil {
		// Stat failures are network-ish: retry via the recoverable path.
		handleFailure(ctx, cfg, msg, payload, markRecoverable(err), logger)
		return
	}
	if projected := projectedFootprint(totalInput); projected > tmpfsBudgetBytes() {
		handleFailure(ctx, cfg, msg, payload, fmt.Errorf("job too large for worker scratch space: projected %d MiB exceeds budget %d MiB (increase TMPFS_BUDGET_MB and the tmpfs mount, or route to a larger worker)", projected/bytesPerMiB, tmpfsBudgetBytes()/bytesPerMiB), logger)
		return
	}
	if totalInput > largeJobThresholdBytes() {
		select {
		case largeJobSem <- struct{}{}:
			defer func() { <-largeJobSem }()
		case <-ctx.Done():
			handleFailure(ctx, cfg, msg, payload, markRecoverable(ctx.Err()), logger)
			return
		}
	}

	localInputs, err := fetchInputs(ctx, cfg.Storage, scratch, payload.InputPaths)
	if err != nil {
		// Download failures are network-ish: retry via the recoverable path.
		handleFailure(ctx, cfg, msg, payload, markRecoverable(err), logger)
		return
	}

	outDir := filepath.Join(scratch, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		handleFailure(ctx, cfg, msg, payload, markRecoverable(fmt.Errorf("create output dir: %w", err)), logger)
		return
	}

	// Per-job timeout prevents hung subprocesses from causing AckWait redelivery.
	jobCtx, jobCancel := context.WithTimeout(ctx, 10*time.Minute)
	defer jobCancel()

	result, procErr := cfg.Process(jobCtx, jobID, payload.ToolType, localInputs, options, outDir)
	if procErr != nil {
		handleFailure(ctx, cfg, msg, payload, procErr, logger)
		return
	}

	// Persist the output to the outputs bucket; the scratch copy is deleted
	// when this function returns.
	outKey, outSize, err := storeOutput(ctx, cfg.Storage, payload.JobID, result.OutputPath)
	if err != nil {
		logger.Error("failed to upload output", "jobId", payload.JobID, "error", err)
		// Upload failures are network-ish: retry via the recoverable path.
		handleFailure(ctx, cfg, msg, payload, markRecoverable(err), logger)
		return
	}

	if err := recordOutput(cfg.DB, jobID, outKey, outSize); err != nil {
		logger.Error("failed to record output", "jobId", payload.JobID, "error", err)
		handleFailure(ctx, cfg, msg, payload, err, logger)
		return
	}

	mergeMetadata(cfg.DB, jobID, result.Metadata, logger)

	// Populate the result cache so future identical jobs short-circuit. TTL is
	// kept at/below the output object lifecycle; the on-hit existence check
	// guards against drift.
	if cacheable && cacheKey != "" {
		if data, err := json.Marshal(cachedResult{OutputKey: outKey, Metadata: result.Metadata}); err == nil {
			cache.set(ctx, cacheKey, string(data), ttl)
		}
	}

	updateJobStatus(cfg.DB, jobID, "completed", 100, "")
	publishStatusEvent(ctx, cfg.JS, jobID, payload.ToolType, "completed", 100, "", payload.QueuedAt, outSize)
	clearFailure(cfg.DB, jobID)

	if err := msg.Ack(); err != nil {
		logger.ErrorContext(ctx, "failed to ack message", "jobId", payload.JobID, "error", err)
	}
	logger.InfoContext(ctx, "job completed", "jobId", payload.JobID, "correlationId", payload.CorrelationID)
}

// tryServeFromCache attempts to satisfy the job from a previously cached
// result. It returns true only when the job has been fully completed (output
// materialised, recorded, status set, event published) — the caller then acks.
// Any failure (cache miss, TTL-expired output, copy/record error) returns false
// so the caller falls through to a normal conversion.
func tryServeFromCache(ctx context.Context, cfg WorkerConfig, cache cacheStore, key string, payload JobPayload, jobID uuid.UUID, logger *slog.Logger) bool {
	raw, ok := cache.get(ctx, key)
	if !ok {
		return false
	}
	var cv cachedResult
	if err := json.Unmarshal([]byte(raw), &cv); err != nil || cv.OutputKey == "" {
		return false
	}

	// The cached output may have been removed by TTL cleanup; verify before use.
	info, err := cfg.Storage.StatObject(ctx, cfg.Storage.BucketOutputs(), cv.OutputKey)
	if err != nil {
		if storage.IsNotFound(err) {
			logger.Info("cache hit but output expired, recomputing", "jobId", payload.JobID)
		} else {
			logger.Warn("cache: stat output failed, recomputing", "jobId", payload.JobID, "error", err)
		}
		return false
	}

	dstKey := "jobs/" + payload.JobID + "/" + path.Base(cv.OutputKey)
	if err := cfg.Storage.CopyObject(ctx, cfg.Storage.BucketOutputs(), cv.OutputKey, cfg.Storage.BucketOutputs(), dstKey); err != nil {
		logger.Warn("cache: copy output failed, recomputing", "jobId", payload.JobID, "error", err)
		return false
	}
	if err := recordOutput(cfg.DB, jobID, dstKey, info.Size); err != nil {
		logger.Warn("cache: record output failed, recomputing", "jobId", payload.JobID, "error", err)
		return false
	}

	mergeMetadata(cfg.DB, jobID, cv.Metadata, logger)
	updateJobStatus(cfg.DB, jobID, "completed", 100, "")
	publishStatusEvent(ctx, cfg.JS, jobID, payload.ToolType, "completed", 100, "", payload.QueuedAt, info.Size)
	clearFailure(cfg.DB, jobID)
	logger.InfoContext(ctx, "job completed from cache", "jobId", payload.JobID, "correlationId", payload.CorrelationID)
	return true
}

func handleFailure(ctx context.Context, cfg WorkerConfig, msg jetstream.Msg, payload JobPayload, err error, logger *slog.Logger) {
	meta, _ := msg.Metadata()
	deliveryCount := uint64(1)
	if meta != nil {
		deliveryCount = meta.NumDelivered
	}

	recoverable := isRecoverable(err)
	// MaxDeliver is 4 (1 initial + 3 retries). If deliveryCount < 4 and the
	// error is recoverable, NAK with delay so the server redelivers.
	if recoverable && deliveryCount < 4 {
		logger.WarnContext(ctx, "job retry",
			"jobId", payload.JobID,
			"delivery", deliveryCount,
			"correlationId", payload.CorrelationID,
			"error", err,
		)
		updateJobStatusString(cfg.DB, payload.JobID, "queued", 0, fmt.Sprintf("retrying: %v", err))
		if parsed, parseErr := uuid.Parse(payload.JobID); parseErr == nil {
			publishStatusEvent(ctx, cfg.JS, parsed, payload.ToolType, "queued", 0, fmt.Sprintf("retrying: %v", err), payload.QueuedAt)
		}
		delay := backoffDuration(int(deliveryCount) - 1)
		if nakErr := msg.NakWithDelay(delay); nakErr != nil {
			logger.ErrorContext(ctx, "failed to nak message", "jobId", payload.JobID, "error", nakErr)
		}
		return
	}

	// Non-recoverable or retries exhausted – ack to stop redelivery.
	logger.ErrorContext(ctx, "job failed",
		"jobId", payload.JobID,
		"delivery", deliveryCount,
		"correlationId", payload.CorrelationID,
		"error", err,
	)
	errCode := classifyError(err)
	failMsg := friendlyMessage(errCode)
	updateJobStatusString(cfg.DB, payload.JobID, "failed", 0, failMsg)
	if parsed, parseErr := uuid.Parse(payload.JobID); parseErr == nil {
		publishStatusEvent(ctx, cfg.JS, parsed, payload.ToolType, "failed", 0, failMsg, payload.QueuedAt)
	}

	// Publish to DLQ before acking. PublishRaw preserves the original message
	// headers (trace context) so the dead-lettered copy keeps its trace, and
	// WithoutCancel guarantees the DLQ hop survives a cancelled job ctx.
	dlqSubject := "jobs.dlq." + cfg.ServiceName
	if cfg.JS != nil {
		dlqPayload := payload
		dlqPayload.EventType = "JobFailed"
		dlqPayload.Attempts = int(deliveryCount)
		if dlqData, marshalErr := json.Marshal(dlqPayload); marshalErr == nil {
			if pubErr := queue.PublishRaw(context.WithoutCancel(ctx), cfg.JS, dlqSubject, dlqData, msg.Headers()); pubErr != nil {
				logger.ErrorContext(ctx, "failed to publish to DLQ", "jobId", payload.JobID, "error", pubErr)
			}
		}
	}

	if ackErr := msg.Ack(); ackErr != nil {
		logger.ErrorContext(ctx, "failed to ack failed message", "jobId", payload.JobID, "error", ackErr)
	}
}

// ─── DB helpers (unchanged) ─────────────────────────────────────────────────

func updateJobStatus(db *gorm.DB, jobID uuid.UUID, status string, progress int, failureReason string) {
	updates := map[string]interface{}{
		"status":   status,
		"progress": progress,
	}
	if status == "completed" {
		now := time.Now().UTC()
		updates["completed_at"] = &now
		// Clear any previous failure reason in the same UPDATE to avoid an
		// extra round trip.
		updates["failure_reason"] = nil
	}
	if failureReason != "" {
		updates["failure_reason"] = failureReason
	}
	if err := db.Model(&models.ProcessingJob{}).Where("id = ?", jobID).Updates(updates).Error; err != nil {
		slog.Error("failed to update job status", "jobId", jobID, "error", err)
	}
}

func updateJobStatusString(db *gorm.DB, jobID string, status string, progress int, failureReason string) {
	parsed, err := uuid.Parse(jobID)
	if err != nil {
		slog.Warn("updateJobStatusString: invalid uuid", "jobId", jobID, "error", err)
		return
	}
	updateJobStatus(db, parsed, status, progress, failureReason)
}

func clearFailure(db *gorm.DB, jobID uuid.UUID) {
	if err := db.Model(&models.ProcessingJob{}).Where("id = ?", jobID).Update("failure_reason", nil).Error; err != nil {
		slog.Error("failed to clear failure reason", "jobId", jobID, "error", err)
	}
}

func mergeMetadata(db *gorm.DB, jobID uuid.UUID, meta map[string]interface{}, logger *slog.Logger) {
	if meta == nil {
		return
	}
	data, err := json.Marshal(meta)
	if err != nil {
		logger.Error("failed to marshal metadata", "jobId", jobID, "error", err)
		return
	}
	// Single atomic UPDATE using the PostgreSQL JSONB merge operator (||)
	// instead of SELECT-modify-UPDATE, which races with concurrent metadata
	// writes (a lost update). COALESCE handles NULL existing metadata.
	result := db.Exec(
		`UPDATE processing_jobs SET metadata = COALESCE(metadata, '{}'::jsonb) || ?::jsonb, updated_at = NOW() WHERE id = ?`,
		string(data), jobID,
	)
	if result.Error != nil {
		logger.Error("failed to update metadata", "jobId", jobID, "error", result.Error)
	}
}

// recordOutput stores the output's object-storage key (outputs bucket) and
// uploaded size, replacing any previous output record from an earlier attempt.
func recordOutput(db *gorm.DB, jobID uuid.UUID, outputKey string, sizeBytes int64) error {
	if outputKey == "" {
		return errors.New("output key is empty")
	}
	if err := db.Where("job_id = ? AND kind = ?", jobID, "output").Delete(&models.FileMetadata{}).Error; err != nil {
		slog.Warn("failed to delete old output record", "jobId", jobID, "error", err)
	}
	output := models.FileMetadata{
		ID:           uuid.New(),
		JobID:        jobID,
		Kind:         "output",
		OriginalName: path.Base(outputKey),
		Path:         outputKey,
		SizeBytes:    sizeBytes,
	}
	return db.Create(&output).Error
}

func isRecoverable(err error) bool {
	if err == nil {
		return false
	}
	var recErr *recoverableError
	if errors.As(err, &recErr) {
		return true
	}
	if strings.Contains(err.Error(), "status=5") || strings.Contains(err.Error(), "status=429") {
		return true
	}
	var netErr interface{ Timeout() bool }
	if errors.As(err, &netErr) {
		return true
	}
	return false
}

// statusToEventType maps a DB status string to a NATS event type.
func statusToEventType(status string) string {
	switch status {
	case "processing":
		return "JobProgress"
	case "completed":
		return "JobCompleted"
	case "failed":
		return "JobFailed"
	case "queued":
		return "JobQueued"
	default:
		return "JobProgress"
	}
}

// publishStatusEvent publishes a job status update to NATS so that SSE
// consumers receive real-time updates without polling. ctx carries the job's
// trace context (the event rides in the same trace); cancellation is stripped
// so a cancelled job still gets its terminal event out. queuedAt echoes the
// dispatch timestamp so terminal events feed job_duration_seconds.
func publishStatusEvent(ctx context.Context, js jetstream.JetStream, jobID uuid.UUID, toolType, status string, progress int, failureReason string, queuedAt time.Time, fileSize ...int64) {
	if js == nil {
		return
	}
	eventType := statusToEventType(status)
	event := queue.JobEvent{
		EventType:     eventType,
		JobID:         jobID.String(),
		ToolType:      toolType,
		Progress:      progress,
		FailureReason: failureReason,
		Timestamp:     time.Now().UTC(),
		QueuedAt:      queuedAt,
	}
	if len(fileSize) > 0 {
		event.FileSize = fileSize[0]
	}
	subject := queue.SubjectForJobEvent(jobID.String(), eventType)
	if err := queue.PublishJobEvent(context.WithoutCancel(ctx), js, subject, event); err != nil {
		slog.WarnContext(ctx, "failed to publish job event", "jobId", jobID, "event", eventType, "error", err)
	}
}

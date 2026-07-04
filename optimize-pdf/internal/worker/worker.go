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
	"gorm.io/gorm"

	"optimize-pdf/internal/models"

	"fyredocs/shared/queue"
	"fyredocs/shared/storage"
)

type ProcessResult struct {
	OutputPath string
	Metadata   map[string]interface{}
}

type ProcessFunc func(ctx context.Context, jobID uuid.UUID, toolType string, inputPaths []string, options map[string]interface{}, outputDir string) (*ProcessResult, error)

type WorkerConfig struct {
	ServiceName  string
	AllowedTools map[string]bool
	Process      ProcessFunc
	JS           jetstream.JetStream
	DB           *gorm.DB
	Storage      Storage       // object storage for job inputs/outputs
	RedisClient  *redis.Client // optional – only used for processing markers; may be nil
}

type JobPayload struct {
	EventType     string          `json:"eventType"`
	JobID         string          `json:"jobId"`
	ToolType      string          `json:"toolType"`
	InputPaths    []string        `json:"inputPaths,omitempty"`
	Options       json.RawMessage `json:"options,omitempty"`
	Attempts      int             `json:"attempts"`
	CorrelationID string          `json:"correlationId"`
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

func Run(ctx context.Context, cfg WorkerConfig) {
	logger := slog.Default().With("service", cfg.ServiceName)

	maxConcurrency := concurrencyFromEnv()
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup

	logger.Info("worker starting", "concurrency", maxConcurrency)

	// ── Create / get durable pull consumer on JOBS_DISPATCH stream ──
	cons, err := cfg.JS.CreateOrUpdateConsumer(ctx, "JOBS_DISPATCH", jetstream.ConsumerConfig{
		Durable:       cfg.ServiceName,
		FilterSubject: "jobs.dispatch." + cfg.ServiceName,
		AckPolicy:     jetstream.AckExplicitPolicy,
		MaxDeliver:    4, // 1 initial + 3 retries
		AckWait:       30 * time.Minute,
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
			logger.Info("worker shutting down, waiting for in-flight jobs")
			wg.Wait()
			return
		default:
		}

		// Pull up to maxConcurrency messages; jobs process in parallel goroutines
		// bounded by the semaphore so one slow OCR job never blocks the rest.
		msgs, err := cons.Fetch(maxConcurrency, jetstream.FetchMaxWait(30*time.Second))
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				wg.Wait()
				return
			}
			logger.Error("fetch failed", "error", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}

		for msg := range msgs.Messages() {
			sem <- struct{}{} // acquire semaphore slot
			wg.Add(1)
			go func(m jetstream.Msg) {
				defer wg.Done()
				defer func() { <-sem }() // release semaphore slot
				processMessage(ctx, cfg, m, logger)
			}(msg)
		}

		if msgs.Error() != nil && !errors.Is(msgs.Error(), jetstream.ErrNoMessages) {
			logger.Error("message batch error", "error", msgs.Error())
		}
	}
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

func processMessage(ctx context.Context, cfg WorkerConfig, msg jetstream.Msg, logger *slog.Logger) {
	var payload JobPayload
	if err := json.Unmarshal(msg.Data(), &payload); err != nil {
		logger.Error("invalid payload", "error", err, "code", ErrCodeInvalidPayload)
		// Non-recoverable: ack to stop redelivery.
		_ = msg.Ack()
		return
	}

	if !cfg.AllowedTools[payload.ToolType] {
		logger.Warn("unsupported tool", "tool", payload.ToolType, "jobId", payload.JobID)
		failMsg := friendlyMessage(ErrCodeUnsupportedTool)
		updateJobStatusString(cfg.DB, payload.JobID, "failed", 0, failMsg)
		if parsed, parseErr := uuid.Parse(payload.JobID); parseErr == nil {
			publishStatusEvent(cfg.JS, parsed, payload.ToolType, "failed", 0, failMsg)
		}
		// Non-recoverable: ack to stop redelivery.
		_ = msg.Ack()
		return
	}

	jobID, err := uuid.Parse(payload.JobID)
	if err != nil {
		logger.Error("invalid job id", "jobId", payload.JobID, "err", err)
		_ = msg.Ack()
		return
	}

	// Skip if job is already completed or being processed by another worker
	var existingJob models.ProcessingJob
	if err := cfg.DB.Select("status").Where("id = ?", jobID).First(&existingJob).Error; err == nil {
		if existingJob.Status == "completed" || existingJob.Status == "processing" {
			logger.Info("job already "+existingJob.Status+", skipping duplicate", "jobId", payload.JobID)
			_ = msg.Ack()
			return
		}
	}

	updateJobStatus(cfg.DB, jobID, "processing", 20, "")
	publishStatusEvent(cfg.JS, jobID, payload.ToolType, "processing", 20, "")

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
		handleFailure(cfg, msg, payload, markRecoverable(fmt.Errorf("create scratch dir: %w", err)), logger)
		return
	}
	defer os.RemoveAll(scratch)

	// Capacity guard: reject jobs whose projected scratch footprint would
	// exceed the worker's tmpfs budget, and serialize large jobs so two of them
	// never exhaust the shared scratch area concurrently.
	totalInput, err := totalInputSize(ctx, cfg.Storage, payload.InputPaths)
	if err != nil {
		// Stat failures are network-ish: retry via the recoverable path.
		handleFailure(cfg, msg, payload, markRecoverable(err), logger)
		return
	}
	if projected := projectedFootprint(totalInput); projected > tmpfsBudgetBytes() {
		handleFailure(cfg, msg, payload, fmt.Errorf("job too large for worker scratch space: projected %d MiB exceeds budget %d MiB (increase TMPFS_BUDGET_MB and the tmpfs mount, or route to a larger worker)", projected/bytesPerMiB, tmpfsBudgetBytes()/bytesPerMiB), logger)
		return
	}
	if totalInput > largeJobThresholdBytes() {
		select {
		case largeJobSem <- struct{}{}:
			defer func() { <-largeJobSem }()
		case <-ctx.Done():
			handleFailure(cfg, msg, payload, markRecoverable(ctx.Err()), logger)
			return
		}
	}

	localInputs, err := fetchInputs(ctx, cfg.Storage, scratch, payload.InputPaths)
	if err != nil {
		// Download failures are network-ish: retry via the recoverable path.
		handleFailure(cfg, msg, payload, markRecoverable(err), logger)
		return
	}

	outDir := filepath.Join(scratch, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		handleFailure(cfg, msg, payload, markRecoverable(fmt.Errorf("create output dir: %w", err)), logger)
		return
	}

	// Per-job timeout prevents hung subprocesses from causing AckWait redelivery.
	jobCtx, jobCancel := context.WithTimeout(ctx, 10*time.Minute)
	defer jobCancel()

	result, procErr := cfg.Process(jobCtx, jobID, payload.ToolType, localInputs, options, outDir)
	if procErr != nil {
		handleFailure(cfg, msg, payload, procErr, logger)
		return
	}

	// Persist the output to the outputs bucket; the scratch copy is deleted
	// when this function returns.
	outKey, outSize, err := storeOutput(ctx, cfg.Storage, payload.JobID, result.OutputPath)
	if err != nil {
		logger.Error("failed to upload output", "jobId", payload.JobID, "error", err)
		// Upload failures are network-ish: retry via the recoverable path.
		handleFailure(cfg, msg, payload, markRecoverable(err), logger)
		return
	}

	if err := recordOutput(cfg.DB, jobID, outKey, outSize); err != nil {
		logger.Error("failed to record output", "jobId", payload.JobID, "error", err)
		handleFailure(cfg, msg, payload, err, logger)
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
	publishStatusEvent(cfg.JS, jobID, payload.ToolType, "completed", 100, "", outSize)

	if err := msg.Ack(); err != nil {
		logger.Error("failed to ack message", "jobId", payload.JobID, "error", err)
	}
	logger.Info("job completed", "jobId", payload.JobID, "correlationId", payload.CorrelationID)
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
	publishStatusEvent(cfg.JS, jobID, payload.ToolType, "completed", 100, "", info.Size)
	clearFailure(cfg.DB, jobID)
	logger.Info("job completed from cache", "jobId", payload.JobID, "correlationId", payload.CorrelationID)
	return true
}

func handleFailure(cfg WorkerConfig, msg jetstream.Msg, payload JobPayload, err error, logger *slog.Logger) {
	meta, _ := msg.Metadata()
	deliveryCount := uint64(1)
	if meta != nil {
		deliveryCount = meta.NumDelivered
	}

	recoverable := isRecoverable(err)
	// MaxDeliver is 4 (1 initial + 3 retries). If deliveryCount < 4 and the
	// error is recoverable, NAK with delay so the server redelivers.
	if recoverable && deliveryCount < 4 {
		logger.Warn("job retry",
			"jobId", payload.JobID,
			"delivery", deliveryCount,
			"correlationId", payload.CorrelationID,
			"error", err,
		)
		updateJobStatusString(cfg.DB, payload.JobID, "queued", 0, fmt.Sprintf("retrying: %v", err))
		if parsed, parseErr := uuid.Parse(payload.JobID); parseErr == nil {
			publishStatusEvent(cfg.JS, parsed, payload.ToolType, "queued", 0, fmt.Sprintf("retrying: %v", err))
		}
		delay := backoffDuration(int(deliveryCount) - 1)
		if nakErr := msg.NakWithDelay(delay); nakErr != nil {
			logger.Error("failed to nak message", "jobId", payload.JobID, "error", nakErr)
		}
		return
	}

	// Non-recoverable or retries exhausted – ack to stop redelivery.
	logger.Error("job failed",
		"jobId", payload.JobID,
		"delivery", deliveryCount,
		"correlationId", payload.CorrelationID,
		"error", err,
	)
	errCode := classifyError(err)
	failMsg := friendlyMessage(errCode)
	updateJobStatusString(cfg.DB, payload.JobID, "failed", 0, failMsg)
	if parsed, parseErr := uuid.Parse(payload.JobID); parseErr == nil {
		publishStatusEvent(cfg.JS, parsed, payload.ToolType, "failed", 0, failMsg)
	}

	// Publish to DLQ before acking
	dlqSubject := "jobs.dlq." + cfg.ServiceName
	if cfg.JS != nil {
		dlqPayload := payload
		dlqPayload.EventType = "JobFailed"
		dlqPayload.Attempts = int(deliveryCount)
		if dlqData, marshalErr := json.Marshal(dlqPayload); marshalErr == nil {
			if _, pubErr := cfg.JS.Publish(context.Background(), dlqSubject, dlqData); pubErr != nil {
				logger.Error("failed to publish to DLQ", "jobId", payload.JobID, "error", pubErr)
			}
		}
	}

	if ackErr := msg.Ack(); ackErr != nil {
		logger.Error("failed to ack failed message", "jobId", payload.JobID, "error", ackErr)
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
		// extra round trip (was previously done by a separate clearFailure call).
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
		slog.Warn("updateJobStatusString: invalid uuid", "jobId", jobID, "err", err)
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
	// Single UPDATE using PostgreSQL JSONB merge operator (||) to avoid a
	// separate SELECT round trip. COALESCE handles the case where metadata
	// is NULL.
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
// consumers receive real-time updates without polling.
func publishStatusEvent(js jetstream.JetStream, jobID uuid.UUID, toolType, status string, progress int, failureReason string, fileSize ...int64) {
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
	}
	if len(fileSize) > 0 {
		event.FileSize = fileSize[0]
	}
	subject := queue.SubjectForJobEvent(jobID.String(), eventType)
	if err := queue.PublishJobEvent(context.Background(), js, subject, event); err != nil {
		slog.Warn("failed to publish job event", "jobId", jobID, "event", eventType, "error", err)
	}
}

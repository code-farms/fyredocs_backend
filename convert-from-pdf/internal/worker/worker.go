package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/redis/go-redis/v9"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"convert-from-pdf/internal/models"

	"esydocs/shared/queue"
)

type ProcessResult struct {
	OutputPath string
	Metadata   map[string]interface{}
}

// ProgressFunc reports processing progress as a percentage (0-100).
type ProgressFunc func(percent int)

type ProcessFunc func(ctx context.Context, jobID uuid.UUID, toolType string, inputPaths []string, options map[string]interface{}, outputDir string, onProgress ProgressFunc) (*ProcessResult, error)

type WorkerConfig struct {
	ServiceName  string
	AllowedTools map[string]bool
	Process      ProcessFunc
	JS           jetstream.JetStream
	DB           *gorm.DB
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
	outDir := outputDir()
	logger := slog.Default().With("service", cfg.ServiceName)

	// ── Create / get durable pull consumer on JOBS_DISPATCH stream ──
	cons, err := cfg.JS.CreateOrUpdateConsumer(ctx, "JOBS_DISPATCH", jetstream.ConsumerConfig{
		Durable:       cfg.ServiceName,
		FilterSubject: "jobs.dispatch." + cfg.ServiceName,
		AckPolicy:     jetstream.AckExplicitPolicy,
		MaxDeliver:    4, // 1 initial + 3 retries
		AckWait:       30 * time.Minute,
		BackOff:       []time.Duration{10 * time.Second, 30 * time.Second, 2 * time.Minute},
	})
	if err != nil {
		logger.Error("failed to create NATS consumer", "error", err)
		return
	}

	for {
		select {
		case <-ctx.Done():
			logger.Info("worker shutting down")
			return
		default:
		}

		// Pull one message at a time, blocking up to 30 s.
		msgs, err := cons.Fetch(1, jetstream.FetchMaxWait(30*time.Second))
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}
			logger.Error("fetch failed", "error", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}

		for msg := range msgs.Messages() {
			processMessage(ctx, cfg, msg, outDir, logger)
		}

		if msgs.Error() != nil && !errors.Is(msgs.Error(), jetstream.ErrNoMessages) {
			logger.Error("message batch error", "error", msgs.Error())
		}
	}
}

func processMessage(ctx context.Context, cfg WorkerConfig, msg jetstream.Msg, outDir string, logger *slog.Logger) {
	var payload JobPayload
	if err := json.Unmarshal(msg.Data(), &payload); err != nil {
		logger.Error("invalid payload", "error", err, "code", ErrCodeInvalidPayload)
		// Non-recoverable: ack to stop redelivery.
		_ = msg.Ack()
		return
	}

	if !cfg.AllowedTools[payload.ToolType] {
		logger.Warn("unsupported tool", "tool", payload.ToolType, "jobId", payload.JobID)
		failMsg := fmt.Sprintf("[%s] %s", ErrCodeUnsupportedTool, payload.ToolType)
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
		logger.Error("invalid job id", "jobId", payload.JobID)
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

	// Only increase progress, never decrease on re-entry (retries).
	updateJobStatusNoRegress(cfg.DB, jobID, "processing", 20)
	publishStatusEvent(cfg.JS, jobID, payload.ToolType, "processing", 20, "")

	options := map[string]interface{}{}
	if len(payload.Options) > 0 && json.Valid(payload.Options) {
		if err := json.Unmarshal(payload.Options, &options); err != nil {
			logger.Error("failed to parse job options", "jobId", payload.JobID, "error", err)
		}
	}

	// Per-job timeout prevents hung subprocesses from causing AckWait redelivery.
	jobCtx, jobCancel := context.WithTimeout(ctx, 10*time.Minute)
	defer jobCancel()

	// Progress callback that only increases, never decreases, the DB value.
	onProgress := func(percent int) {
		clamped := percent
		if clamped < 20 {
			clamped = 20
		}
		if clamped > 90 {
			clamped = 90
		}
		if err := cfg.DB.Model(&models.ProcessingJob{}).
			Where("id = ? AND progress < ?", jobID, clamped).
			Update("progress", clamped).Error; err != nil {
			logger.Warn("failed to update progress", "jobId", jobID, "error", err)
		}
		publishStatusEvent(cfg.JS, jobID, payload.ToolType, "processing", clamped, "")
	}

	result, procErr := cfg.Process(jobCtx, jobID, payload.ToolType, payload.InputPaths, options, outDir, onProgress)
	if procErr != nil {
		handleFailure(cfg, msg, payload, procErr, logger)
		return
	}

	if err := recordOutput(cfg.DB, jobID, result.OutputPath); err != nil {
		logger.Error("failed to record output", "jobId", payload.JobID, "error", err)
		handleFailure(cfg, msg, payload, err, logger)
		return
	}

	mergeMetadata(cfg.DB, jobID, result.Metadata, logger)
	updateJobStatus(cfg.DB, jobID, "completed", 100, "")
	publishStatusEvent(cfg.JS, jobID, payload.ToolType, "completed", 100, "", outputFileSize(result.OutputPath))
	clearFailure(cfg.DB, jobID)

	if err := msg.Ack(); err != nil {
		logger.Error("failed to ack message", "jobId", payload.JobID, "error", err)
	}
	logger.Info("job completed", "jobId", payload.JobID, "correlationId", payload.CorrelationID)
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
		// Keep status as "processing" and do NOT reset progress — prevents
		// the frontend from seeing a progress regression during retries.
		retryReason := fmt.Sprintf("retrying (attempt %d): %v", deliveryCount, err)
		if updateErr := cfg.DB.Model(&models.ProcessingJob{}).
			Where("id = ?", payload.JobID).
			Updates(map[string]interface{}{
				"status":         "processing",
				"failure_reason": retryReason,
			}).Error; updateErr != nil {
			logger.Error("failed to update retry status", "jobId", payload.JobID, "error", updateErr)
		}
		if parsed, parseErr := uuid.Parse(payload.JobID); parseErr == nil {
			publishStatusEvent(cfg.JS, parsed, payload.ToolType, "processing", 0, retryReason)
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
	failMsg := fmt.Sprintf("[%s] %v", errCode, err)
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
	}
	if failureReason != "" {
		updates["failure_reason"] = failureReason
	}
	if err := db.Model(&models.ProcessingJob{}).Where("id = ?", jobID).Updates(updates).Error; err != nil {
		slog.Error("failed to update job status", "jobId", jobID, "error", err)
	}
}

// updateJobStatusNoRegress sets status and progress but only if the new
// progress is higher than the current value. This prevents visible regressions
// during retries.
func updateJobStatusNoRegress(db *gorm.DB, jobID uuid.UUID, status string, progress int) {
	if err := db.Model(&models.ProcessingJob{}).
		Where("id = ? AND progress < ?", jobID, progress).
		Updates(map[string]interface{}{
			"status":   status,
			"progress": progress,
		}).Error; err != nil {
		slog.Error("failed to update job status (no-regress)", "jobId", jobID, "error", err)
	}
	// Also update status even if progress didn't change (e.g. re-entry at same progress).
	if err := db.Model(&models.ProcessingJob{}).
		Where("id = ?", jobID).
		Update("status", status).Error; err != nil {
		slog.Error("failed to update job status", "jobId", jobID, "error", err)
	}
}

func updateJobStatusString(db *gorm.DB, jobID string, status string, progress int, failureReason string) {
	parsed, err := uuid.Parse(jobID)
	if err != nil {
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
	var job models.ProcessingJob
	if err := db.First(&job, "id = ?", jobID).Error; err != nil {
		logger.Error("failed to load job for metadata merge", "jobId", jobID, "error", err)
		return
	}
	existing := map[string]interface{}{}
	if len(job.Metadata) > 0 {
		if err := json.Unmarshal(job.Metadata, &existing); err != nil {
			logger.Error("failed to unmarshal existing metadata", "jobId", jobID, "error", err)
		}
	}
	for key, value := range meta {
		existing[key] = value
	}
	if data, err := json.Marshal(existing); err == nil {
		if err := db.Model(&models.ProcessingJob{}).Where("id = ?", jobID).Update("metadata", datatypes.JSON(data)).Error; err != nil {
			logger.Error("failed to update metadata", "jobId", jobID, "error", err)
		}
	}
}

func recordOutput(db *gorm.DB, jobID uuid.UUID, outputPath string) error {
	if outputPath == "" {
		return errors.New("output path is empty")
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		return err
	}
	if err := db.Where("job_id = ? AND kind = ?", jobID, "output").Delete(&models.FileMetadata{}).Error; err != nil {
		slog.Warn("failed to delete old output record", "jobId", jobID, "error", err)
	}
	output := models.FileMetadata{
		ID:           uuid.New(),
		JobID:        jobID,
		Kind:         "output",
		OriginalName: filepathBase(outputPath),
		Path:         outputPath,
		SizeBytes:    info.Size(),
	}
	return db.Create(&output).Error
}

func filepathBase(path string) string {
	idx := strings.LastIndex(path, string(os.PathSeparator))
	if idx == -1 {
		return path
	}
	return path[idx+1:]
}

func outputDir() string {
	if value := os.Getenv("OUTPUT_DIR"); value != "" {
		return value
	}
	return "outputs"
}

func isRecoverable(err error) bool {
	if err == nil {
		return false
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

// outputFileSize returns the size of the file at path, or 0 on error.
func outputFileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
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

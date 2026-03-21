package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/redis/go-redis/v9"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"convert-to-pdf/internal/models"

	"esydocs/shared/queue"
)

type ProcessResult struct {
	OutputPath string
	Metadata   map[string]interface{}
}

// ProgressFunc is called to report real progress (current item out of total).
type ProgressFunc func(current, total int)

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

// ─── Progress reporter ──────────────────────────────────────────────────────

// progressReporter smoothly updates job progress in the background based on
// estimated conversion time derived from input file size.
type progressReporter struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// startProgressReporter launches a goroutine that increments progress from
// startPct to maxPct over estimatedDuration, updating the DB every tick.
// It uses a sqrt ease-out curve so progress feels natural (fast at first,
// slows as it approaches maxPct). Both DB writes and NATS events are
// published every 3s.
func startProgressReporter(db *gorm.DB, js jetstream.JetStream, jobID uuid.UUID, toolType string, startPct, maxPct int, estimatedDuration time.Duration) *progressReporter {
	ctx, cancel := context.WithCancel(context.Background())
	pr := &progressReporter{cancel: cancel, done: make(chan struct{})}

	go func() {
		defer close(pr.done)
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		start := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ratio := float64(time.Since(start)) / float64(estimatedDuration)
				if ratio > 1 {
					ratio = 1
				}
				eased := math.Sqrt(ratio) // ease-out curve
				pct := startPct + int(eased*float64(maxPct-startPct))
				if pct > maxPct {
					pct = maxPct
				}
				updateJobStatus(db, jobID, "processing", pct, "")
				publishStatusEvent(js, jobID, toolType, "processing", pct, "")
			}
		}
	}()
	return pr
}

// stop cancels the reporter goroutine and waits for it to exit.
func (pr *progressReporter) stop() {
	pr.cancel()
	<-pr.done
}

// estimateConversionTime returns an estimated duration based on total input
// file size and tool type. Office conversions are slower due to LibreOffice
// overhead; pdfcpu operations are pure Go and much faster.
func estimateConversionTime(toolType string, inputPaths []string) time.Duration {
	var totalBytes int64
	for _, p := range inputPaths {
		if info, err := os.Stat(p); err == nil {
			totalBytes += info.Size()
		}
	}
	mb := float64(totalBytes) / (1024 * 1024)

	if isOfficeConversion(toolType) {
		secs := 2.0 + mb*1.5 // base 2s + 1.5s per MB
		return time.Duration(secs * float64(time.Second))
	}
	// pdfcpu operations are fast
	secs := 0.5 + mb*0.3 // base 0.5s + 0.3s per MB
	return time.Duration(secs * float64(time.Second))
}

// isOfficeConversion returns true for tool types that use LibreOffice.
func isOfficeConversion(toolType string) bool {
	switch toolType {
	case "word-to-pdf", "excel-to-pdf", "ppt-to-pdf", "html-to-pdf":
		return true
	}
	return false
}

// hasRealProgress returns true for tool types that report real page-by-page
// progress via the ProgressFunc callback (i.e., operations with internal loops).
func hasRealProgress(toolType string) bool {
	switch toolType {
	case "split-pdf", "add-page-numbers", "edit-pdf":
		return true
	}
	return false
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

func Run(ctx context.Context, cfg WorkerConfig) {
	outDir := outputDir()
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
				processMessage(ctx, cfg, m, outDir, logger)
			}(msg)
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

	updateJobStatus(cfg.DB, jobID, "processing", 20, "")
	publishStatusEvent(cfg.JS, jobID, payload.ToolType, "processing", 20, "")

	options := map[string]interface{}{}
	if len(payload.Options) > 0 && json.Valid(payload.Options) {
		if err := json.Unmarshal(payload.Options, &options); err != nil {
			logger.Error("failed to parse job options", "jobId", payload.JobID, "error", err)
		}
	}

	var result *ProcessResult
	var procErr error

	if hasRealProgress(payload.ToolType) {
		// Real progress: callback updates DB with actual current/total.
		onProgress := func(current, total int) {
			if total <= 0 {
				return
			}
			pct := 20 + int(float64(current)/float64(total)*70) // scale to 20%–90%
			if pct > 90 {
				pct = 90
			}
			updateJobStatus(cfg.DB, jobID, "processing", pct, "")
			publishStatusEvent(cfg.JS, jobID, payload.ToolType, "processing", pct, "")
		}
		result, procErr = cfg.Process(ctx, jobID, payload.ToolType, payload.InputPaths, options, outDir, onProgress)
	} else {
		// Time-based estimation for office conversions and single-pass operations.
		estimated := estimateConversionTime(payload.ToolType, payload.InputPaths)
		reporter := startProgressReporter(cfg.DB, cfg.JS, jobID, payload.ToolType, 20, 90, estimated)
		result, procErr = cfg.Process(ctx, jobID, payload.ToolType, payload.InputPaths, options, outDir, nil)
		reporter.stop()
	}

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
	publishStatusEvent(cfg.JS, jobID, payload.ToolType, "completed", 100, "")
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

// publishStatusEvent publishes a job status update to NATS so that SSE
// consumers receive real-time updates without polling.
func publishStatusEvent(js jetstream.JetStream, jobID uuid.UUID, toolType, status string, progress int, failureReason string) {
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
	subject := queue.SubjectForJobEvent(jobID.String(), eventType)
	if err := queue.PublishJobEvent(context.Background(), js, subject, event); err != nil {
		slog.Warn("failed to publish job event", "jobId", jobID, "event", eventType, "error", err)
	}
}

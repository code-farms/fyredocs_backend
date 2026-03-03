package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"esydocs/shared/database"
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
	RedisClient  *redis.Client
	DB           *gorm.DB
}

type JobPayload struct {
	JobID         string          `json:"jobId"`
	ToolType      string          `json:"toolType"`
	InputPaths    []string        `json:"inputPaths"`
	Options       json.RawMessage `json:"options,omitempty"`
	Attempts      int             `json:"attempts"`
	CorrelationID string          `json:"correlationId"`
}

func Run(ctx context.Context, cfg WorkerConfig) {
	qName := queueName(cfg.ServiceName)
	processingList := qName + ":processing"
	timeout := processingTimeout()
	outDir := outputDir()
	retries := maxRetries()
	logger := slog.Default().With("service", cfg.ServiceName)

	for {
		select {
		case <-ctx.Done():
			logger.Info("worker shutting down")
			return
		default:
		}

		// Fix #13: Use BLMove instead of deprecated BRPopLPush
		payloadJSON, err := cfg.RedisClient.BLMove(ctx, qName, processingList, "RIGHT", "LEFT", 0).Result()
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}
			logger.Error("queue pop failed", "error", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}

		var payload JobPayload
		if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
			logger.Error("invalid payload", "error", err)
			ackProcessing(ctx, cfg.RedisClient, processingList, payloadJSON)
			continue
		}

		if !cfg.AllowedTools[payload.ToolType] {
			logger.Warn("unsupported tool", "tool", payload.ToolType, "jobId", payload.JobID)
			markFailed(ctx, cfg, payload, "unsupported tool", payloadJSON, processingList)
			continue
		}

		jobID, err := uuid.Parse(payload.JobID)
		if err != nil {
			logger.Error("invalid job id", "jobId", payload.JobID)
			ackProcessing(ctx, cfg.RedisClient, processingList, payloadJSON)
			continue
		}

		setProcessingMarker(ctx, cfg.RedisClient, payload, timeout)
		updateJobStatus(cfg.DB, jobID, "processing", 20, "")

		options := map[string]interface{}{}
		if len(payload.Options) > 0 && json.Valid(payload.Options) {
			if err := json.Unmarshal(payload.Options, &options); err != nil {
				logger.Error("failed to parse job options", "jobId", payload.JobID, "error", err)
			}
		}

		result, err := cfg.Process(ctx, jobID, payload.ToolType, payload.InputPaths, options, outDir)
		if err != nil {
			handleFailure(ctx, cfg, payload, err, payloadJSON, processingList, retries, logger)
			continue
		}

		if err := recordOutput(cfg.DB, jobID, result.OutputPath); err != nil {
			logger.Error("failed to record output", "jobId", payload.JobID, "error", err)
			handleFailure(ctx, cfg, payload, err, payloadJSON, processingList, retries, logger)
			continue
		}

		mergeMetadata(cfg.DB, jobID, result.Metadata, logger)
		updateJobStatus(cfg.DB, jobID, "completed", 100, "")
		clearFailure(cfg.DB, jobID)

		ackProcessing(ctx, cfg.RedisClient, processingList, payloadJSON)
		clearProcessingMarker(ctx, cfg.RedisClient, payload.JobID)
		logger.Info("job completed", "jobId", payload.JobID, "correlationId", payload.CorrelationID)
	}
}

func handleFailure(ctx context.Context, cfg WorkerConfig, payload JobPayload, err error, payloadJSON string, processingList string, maxRetries int, logger *slog.Logger) {
	payload.Attempts++
	recoverable := isRecoverable(err)
	if recoverable && payload.Attempts < maxRetries {
		logger.Warn("job retry", "jobId", payload.JobID, "attempt", payload.Attempts, "correlationId", payload.CorrelationID, "error", err)
		updateJobStatusString(cfg.DB, payload.JobID, "queued", 0, fmt.Sprintf("retrying: %v", err))
		ackProcessing(ctx, cfg.RedisClient, processingList, payloadJSON)
		clearProcessingMarker(ctx, cfg.RedisClient, payload.JobID)
		newPayload, _ := json.Marshal(payload)
		if err := cfg.RedisClient.LPush(ctx, queueName(cfg.ServiceName), newPayload).Err(); err != nil {
			logger.Error("failed to requeue job", "jobId", payload.JobID, "error", err)
		}
		return
	}

	logger.Error("job failed", "jobId", payload.JobID, "attempt", payload.Attempts, "correlationId", payload.CorrelationID, "error", err)
	updateJobStatusString(cfg.DB, payload.JobID, "failed", 0, err.Error())
	ackProcessing(ctx, cfg.RedisClient, processingList, payloadJSON)
	clearProcessingMarker(ctx, cfg.RedisClient, payload.JobID)
}

func markFailed(ctx context.Context, cfg WorkerConfig, payload JobPayload, reason string, payloadJSON string, processingList string) {
	updateJobStatusString(cfg.DB, payload.JobID, "failed", 0, reason)
	ackProcessing(ctx, cfg.RedisClient, processingList, payloadJSON)
	clearProcessingMarker(ctx, cfg.RedisClient, payload.JobID)
}

// Fix #12: progress is now int
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
	if err := db.Model(&database.ProcessingJob{}).Where("id = ?", jobID).Updates(updates).Error; err != nil {
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
	if err := db.Model(&database.ProcessingJob{}).Where("id = ?", jobID).Update("failure_reason", nil).Error; err != nil {
		slog.Error("failed to clear failure reason", "jobId", jobID, "error", err)
	}
}

func mergeMetadata(db *gorm.DB, jobID uuid.UUID, meta map[string]interface{}, logger *slog.Logger) {
	if meta == nil {
		return
	}
	var job database.ProcessingJob
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
		if err := db.Model(&database.ProcessingJob{}).Where("id = ?", jobID).Update("metadata", datatypes.JSON(data)).Error; err != nil {
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
	if err := db.Where("job_id = ? AND kind = ?", jobID, "output").Delete(&database.FileMetadata{}).Error; err != nil {
		slog.Warn("failed to delete old output record", "jobId", jobID, "error", err)
	}
	output := database.FileMetadata{
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

func queueName(serviceName string) string {
	prefix := os.Getenv("QUEUE_PREFIX")
	if prefix == "" {
		prefix = "queue"
	}
	return fmt.Sprintf("%s:%s", prefix, serviceName)
}

func processingTimeout() time.Duration {
	value := os.Getenv("PROCESSING_TIMEOUT")
	if value == "" {
		return 30 * time.Minute
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 30 * time.Minute
	}
	return parsed
}

func outputDir() string {
	if value := os.Getenv("OUTPUT_DIR"); value != "" {
		return value
	}
	return "outputs"
}

func maxRetries() int {
	value := os.Getenv("MAX_RETRIES")
	if value == "" {
		return 3
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 3
	}
	return parsed
}

func setProcessingMarker(ctx context.Context, client *redis.Client, payload JobPayload, timeout time.Duration) {
	if client == nil {
		return
	}
	key := fmt.Sprintf("processing:%s", payload.JobID)
	if err := client.HSet(ctx, key, map[string]interface{}{
		"startedAt": time.Now().UTC().Format(time.RFC3339),
		"attempts":  payload.Attempts,
		"toolType":  payload.ToolType,
	}).Err(); err != nil {
		slog.Error("failed to set processing marker", "jobId", payload.JobID, "error", err)
	}
	if err := client.Expire(ctx, key, timeout).Err(); err != nil {
		slog.Error("failed to set processing marker expiry", "jobId", payload.JobID, "error", err)
	}
}

func clearProcessingMarker(ctx context.Context, client *redis.Client, jobID string) {
	if client == nil {
		return
	}
	key := fmt.Sprintf("processing:%s", jobID)
	if err := client.Del(ctx, key).Err(); err != nil {
		slog.Error("failed to clear processing marker", "jobId", jobID, "error", err)
	}
}

func ackProcessing(ctx context.Context, client *redis.Client, processingList string, payloadJSON string) {
	if err := client.LRem(ctx, processingList, 1, payloadJSON).Err(); err != nil {
		slog.Error("failed to ack processing", "error", err)
	}
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

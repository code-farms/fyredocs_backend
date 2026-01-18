package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"

	"organize-pdf/database"
	"organize-pdf/processing"
	"organize-pdf/redisstore"
)

type JobPayload struct {
	JobID         string          `json:"jobId"`
	ToolType      string          `json:"toolType"`
	InputPaths    []string        `json:"inputPaths"`
	Options       json.RawMessage `json:"options,omitempty"`
	Attempts      int             `json:"attempts"`
	CorrelationID string          `json:"correlationId"`
}

var allowedTools = map[string]bool{
	"merge-pdf":     true,
	"split-pdf":     true,
	"remove-pages":  true,
	"extract-pages": true,
	"organize-pdf":  true,
	"scan-to-pdf":   true,
}

func Run(ctx context.Context) {
	queueName := queueName()
	processingList := queueName + ":processing"
	timeout := processingTimeout()
	outputDir := outputDir()
	maxRetries := maxRetries()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		payloadJSON, err := redisstore.Client.BRPopLPush(ctx, queueName, processingList, 0).Result()
		if err != nil {
			log.Printf("queue pop failed: %v", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}

		var payload JobPayload
		if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
			log.Printf("invalid payload: %v", err)
			ackProcessing(ctx, processingList, payloadJSON)
			continue
		}

		if !allowedTools[payload.ToolType] {
			log.Printf("unsupported tool in worker: %s jobId=%s", payload.ToolType, payload.JobID)
			markFailed(ctx, payload, "unsupported tool", payloadJSON, processingList)
			continue
		}

		jobID, err := uuid.Parse(payload.JobID)
		if err != nil {
			log.Printf("invalid job id: %s", payload.JobID)
			ackProcessing(ctx, processingList, payloadJSON)
			continue
		}

		setProcessingMarker(ctx, payload, timeout)
		updateJobStatus(jobID, "processing", "20", "")

		options := map[string]interface{}{}
		if len(payload.Options) > 0 && json.Valid(payload.Options) {
			_ = json.Unmarshal(payload.Options, &options)
		}

		result, err := processing.ProcessFile(jobID, payload.ToolType, payload.InputPaths, options, outputDir)
		if err != nil {
			handleFailure(ctx, payload, err, payloadJSON, processingList, maxRetries)
			continue
		}

		if err := recordOutput(jobID, result.OutputPath); err != nil {
			handleFailure(ctx, payload, err, payloadJSON, processingList, maxRetries)
			continue
		}

		mergeMetadata(jobID, result.Metadata)
		updateJobStatus(jobID, "completed", "100", "")
		clearFailure(jobID)

		ackProcessing(ctx, processingList, payloadJSON)
		clearProcessingMarker(ctx, payload.JobID)
		log.Printf("job completed jobId=%s correlationId=%s", payload.JobID, payload.CorrelationID)
	}
}

func handleFailure(ctx context.Context, payload JobPayload, err error, payloadJSON string, processingList string, maxRetries int) {
	payload.Attempts++
	recoverable := isRecoverable(err)
	if recoverable && payload.Attempts <= maxRetries {
		log.Printf("job retry jobId=%s attempt=%d correlationId=%s err=%v", payload.JobID, payload.Attempts, payload.CorrelationID, err)
		updateJobStatusString(payload.JobID, "queued", "0", fmt.Sprintf("retrying: %v", err))
		ackProcessing(ctx, processingList, payloadJSON)
		clearProcessingMarker(ctx, payload.JobID)
		newPayload, _ := json.Marshal(payload)
		_ = redisstore.Client.LPush(ctx, queueName(), newPayload).Err()
		return
	}

	log.Printf("job failed jobId=%s attempt=%d correlationId=%s err=%v", payload.JobID, payload.Attempts, payload.CorrelationID, err)
	updateJobStatusString(payload.JobID, "failed", "0", err.Error())
	ackProcessing(ctx, processingList, payloadJSON)
	clearProcessingMarker(ctx, payload.JobID)
}

func markFailed(ctx context.Context, payload JobPayload, reason string, payloadJSON string, processingList string) {
	updateJobStatusString(payload.JobID, "failed", "0", reason)
	ackProcessing(ctx, processingList, payloadJSON)
	clearProcessingMarker(ctx, payload.JobID)
}

func updateJobStatus(jobID uuid.UUID, status string, progress string, failureReason string) {
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
	database.DB.Model(&database.ProcessingJob{}).Where("id = ?", jobID).Updates(updates)
}

func updateJobStatusString(jobID string, status string, progress string, failureReason string) {
	parsed, err := uuid.Parse(jobID)
	if err != nil {
		return
	}
	updateJobStatus(parsed, status, progress, failureReason)
}

func clearFailure(jobID uuid.UUID) {
	database.DB.Model(&database.ProcessingJob{}).Where("id = ?", jobID).Update("failure_reason", nil)
}

func mergeMetadata(jobID uuid.UUID, meta map[string]interface{}) {
	if meta == nil {
		return
	}
	var job database.ProcessingJob
	if err := database.DB.First(&job, "id = ?", jobID).Error; err != nil {
		return
	}
	existing := map[string]interface{}{}
	if len(job.Metadata) > 0 {
		_ = json.Unmarshal(job.Metadata, &existing)
	}
	for key, value := range meta {
		existing[key] = value
	}
	if data, err := json.Marshal(existing); err == nil {
		database.DB.Model(&database.ProcessingJob{}).Where("id = ?", jobID).Update("metadata", datatypes.JSON(data))
	}
}

func recordOutput(jobID uuid.UUID, outputPath string) error {
	if outputPath == "" {
		return errors.New("output path is empty")
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		return err
	}
	_ = database.DB.Where("job_id = ? AND kind = ?", jobID, "output").Delete(&database.FileMetadata{}).Error
	output := database.FileMetadata{
		ID:           uuid.New(),
		JobID:        jobID,
		Kind:         "output",
		OriginalName: filepathBase(outputPath),
		Path:         outputPath,
		SizeBytes:    info.Size(),
	}
	return database.DB.Create(&output).Error
}

func filepathBase(path string) string {
	idx := strings.LastIndex(path, string(os.PathSeparator))
	if idx == -1 {
		return path
	}
	return path[idx+1:]
}

func queueName() string {
	prefix := os.Getenv("QUEUE_PREFIX")
	if prefix == "" {
		prefix = "queue"
	}
	return fmt.Sprintf("%s:%s", prefix, "organize-pdf")
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

func setProcessingMarker(ctx context.Context, payload JobPayload, timeout time.Duration) {
	key := fmt.Sprintf("processing:%s", payload.JobID)
	redisstore.Client.HSet(ctx, key, map[string]interface{}{
		"startedAt": time.Now().UTC().Format(time.RFC3339),
		"attempts":  payload.Attempts,
		"toolType":  payload.ToolType,
	})
	redisstore.Client.Expire(ctx, key, timeout)
}

func clearProcessingMarker(ctx context.Context, jobID string) {
	key := fmt.Sprintf("processing:%s", jobID)
	redisstore.Client.Del(ctx, key)
}

func ackProcessing(ctx context.Context, processingList string, payloadJSON string) {
	redisstore.Client.LRem(ctx, processingList, 1, payloadJSON)
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

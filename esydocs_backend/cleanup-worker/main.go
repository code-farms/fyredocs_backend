package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"esydocs/shared/config"
	"esydocs/shared/database"
	"esydocs/shared/redisstore"
)

type JobPayload struct {
	JobID         string          `json:"jobId"`
	ToolType      string          `json:"toolType"`
	InputPaths    []string        `json:"inputPaths"`
	Options       json.RawMessage `json:"options,omitempty"`
	Attempts      int             `json:"attempts"`
	CorrelationID string          `json:"correlationId"`
}

func main() {
	config.LoadConfig()
	database.Connect()
	database.Migrate()
	redisstore.Connect()

	interval := cleanupInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	slog.Info("cleanup-worker started", "interval", interval)

	for {
		runCleanup(context.Background())
		<-ticker.C
	}
}

func runCleanup(ctx context.Context) {
	cleanupExpiredJobs(ctx)
	cleanupUploadState(ctx)
	// Fix #9: Requeue stale jobs for ALL services, not just convert-from/to-pdf
	requeueStaleJobs(ctx, queueName("convert-from-pdf"))
	requeueStaleJobs(ctx, queueName("convert-to-pdf"))
	requeueStaleJobs(ctx, queueName("organize-pdf"))
	requeueStaleJobs(ctx, queueName("optimize-pdf"))
	// Fix #7: Recover orphaned jobs stuck in "queued" status
	requeueOrphanedJobs(ctx)
}

// Fix #18: Batch expired job processing with Limit(100) loop
func cleanupExpiredJobs(ctx context.Context) {
	now := time.Now().UTC()
	for {
		var jobs []database.ProcessingJob
		query := database.DB.Where("user_id IS NULL AND expires_at IS NOT NULL AND expires_at <= ?", now).Limit(100)
		if err := query.Find(&jobs).Error; err != nil {
			slog.Error("cleanup jobs query failed", "error", err)
			return
		}
		if len(jobs) == 0 {
			return
		}

		for _, job := range jobs {
			var files []database.FileMetadata
			if err := database.DB.Where("job_id = ?", job.ID).Find(&files).Error; err != nil {
				slog.Error("failed to find files for job", "jobId", job.ID, "error", err)
			}
			for _, file := range files {
				if err := os.Remove(file.Path); err != nil && !os.IsNotExist(err) {
					slog.Warn("failed to remove file", "path", file.Path, "error", err)
				}
			}
			if err := database.DB.Where("job_id = ?", job.ID).Delete(&database.FileMetadata{}).Error; err != nil {
				slog.Error("failed to delete file metadata", "jobId", job.ID, "error", err)
			}
			if err := database.DB.Delete(&job).Error; err != nil {
				slog.Error("failed to delete job", "jobId", job.ID, "error", err)
			}
		}

		if len(jobs) < 100 {
			return
		}
	}
}

func cleanupUploadState(ctx context.Context) {
	if redisstore.Client == nil {
		return
	}
	// Fix #19: Add count=100 to SCAN call
	iter := redisstore.Client.Scan(ctx, 0, "upload:*", 100).Iterator()
	ttl := uploadTTL()
	for iter.Next(ctx) {
		key := iter.Val()
		if strings.Contains(key, ":chunks") {
			continue
		}
		createdAt, err := redisstore.Client.HGet(ctx, key, "createdAt").Result()
		if err != nil || createdAt == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			continue
		}
		if time.Since(parsed) > ttl {
			if err := redisstore.Client.Del(ctx, key, key+":chunks").Err(); err != nil {
				slog.Warn("failed to delete upload state", "key", key, "error", err)
			}
			uploadID := strings.TrimPrefix(key, "upload:")
			if _, uuidErr := uuid.Parse(uploadID); uuidErr == nil {
				if err := os.RemoveAll(filepath.Join(uploadBaseDir(), "tmp", uploadID)); err != nil {
					slog.Warn("failed to remove upload dir", "uploadId", uploadID, "error", err)
				}
			}
		}
	}
	if err := iter.Err(); err != nil {
		slog.Error("SCAN iterator error", "error", err)
	}
}

func requeueStaleJobs(ctx context.Context, queue string) {
	if redisstore.Client == nil {
		return
	}
	processingList := queue + ":processing"
	items, err := redisstore.Client.LRange(ctx, processingList, 0, -1).Result()
	if err != nil {
		slog.Error("failed to list processing queue", "queue", processingList, "error", err)
		return
	}
	for _, item := range items {
		var payload JobPayload
		if err := json.Unmarshal([]byte(item), &payload); err != nil {
			if err := redisstore.Client.LRem(ctx, processingList, 1, item).Err(); err != nil {
				slog.Error("failed to remove invalid payload", "error", err)
			}
			continue
		}
		if payload.JobID == "" {
			if err := redisstore.Client.LRem(ctx, processingList, 1, item).Err(); err != nil {
				slog.Error("failed to remove empty job payload", "error", err)
			}
			continue
		}
		key := fmt.Sprintf("processing:%s", payload.JobID)
		ttl, err := redisstore.Client.TTL(ctx, key).Result()
		if err == nil && ttl > 0 {
			continue
		}

		payload.Attempts++
		if payload.Attempts > maxRetries() {
			markFailed(payload.JobID, "processing timeout")
			if err := redisstore.Client.LRem(ctx, processingList, 1, item).Err(); err != nil {
				slog.Error("failed to ack timed out job", "jobId", payload.JobID, "error", err)
			}
			continue
		}

		if err := redisstore.Client.LRem(ctx, processingList, 1, item).Err(); err != nil {
			slog.Error("failed to remove stale job from processing", "jobId", payload.JobID, "error", err)
		}
		newPayload, _ := json.Marshal(payload)
		if err := redisstore.Client.LPush(ctx, queue, newPayload).Err(); err != nil {
			slog.Error("failed to requeue stale job", "jobId", payload.JobID, "error", err)
		}
		if err := redisstore.Client.Del(ctx, key).Err(); err != nil {
			slog.Error("failed to delete processing marker", "jobId", payload.JobID, "error", err)
		}
		slog.Info("requeued stale job", "jobId", payload.JobID, "attempt", payload.Attempts)
	}
}

// Fix #7: Recover orphaned jobs stuck in "queued" status with no queue entry
func requeueOrphanedJobs(ctx context.Context) {
	if redisstore.Client == nil {
		return
	}
	staleThreshold := time.Now().UTC().Add(-30 * time.Minute)
	var jobs []database.ProcessingJob
	if err := database.DB.Where("status = 'queued' AND updated_at < ?", staleThreshold).Limit(100).Find(&jobs).Error; err != nil {
		slog.Error("failed to query orphaned jobs", "error", err)
		return
	}

	for _, job := range jobs {
		queue := queueName(serviceForTool(job.ToolType))
		if queue == "" {
			continue
		}
		payload := JobPayload{
			JobID:    job.ID.String(),
			ToolType: job.ToolType,
		}
		data, _ := json.Marshal(payload)
		if err := redisstore.Client.LPush(ctx, queue, data).Err(); err != nil {
			slog.Error("failed to requeue orphaned job", "jobId", job.ID, "error", err)
			continue
		}
		slog.Info("requeued orphaned job", "jobId", job.ID, "toolType", job.ToolType)
	}
}

func serviceForTool(toolType string) string {
	switch toolType {
	case "pdf-to-image", "pdf-to-img", "pdf-to-pdfa", "pdf-to-word", "pdf-to-docx",
		"pdf-to-excel", "pdf-to-xlsx", "pdf-to-ppt", "pdf-to-powerpoint", "pdf-to-pptx",
		"pdf-to-html", "pdf-to-text", "pdf-to-txt":
		return "convert-from-pdf"
	case "word-to-pdf", "ppt-to-pdf", "excel-to-pdf", "html-to-pdf",
		"image-to-pdf", "img-to-pdf", "compress-pdf", "merge-pdf", "split-pdf",
		"protect-pdf", "unlock-pdf", "watermark-pdf", "edit-pdf", "sign-pdf":
		return "convert-to-pdf"
	case "remove-pages", "extract-pages", "organize-pdf", "scan-to-pdf":
		return "organize-pdf"
	case "repair-pdf", "ocr-pdf":
		return "optimize-pdf"
	default:
		return ""
	}
}

// Fix #12: progress is now int, not string
func markFailed(jobID string, reason string) {
	parsed, err := uuid.Parse(jobID)
	if err != nil {
		return
	}
	updates := map[string]interface{}{
		"status":         "failed",
		"progress":       0,
		"failure_reason": reason,
	}
	if err := database.DB.Model(&database.ProcessingJob{}).Where("id = ?", parsed).Updates(updates).Error; err != nil {
		slog.Error("failed to mark job as failed", "jobId", jobID, "error", err)
	}
}

func queueName(worker string) string {
	prefix := os.Getenv("QUEUE_PREFIX")
	if prefix == "" {
		prefix = "queue"
	}
	return fmt.Sprintf("%s:%s", prefix, worker)
}

func cleanupInterval() time.Duration {
	value := os.Getenv("CLEANUP_INTERVAL")
	if value == "" {
		return 15 * time.Minute
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 15 * time.Minute
	}
	return parsed
}

func uploadTTL() time.Duration {
	value := os.Getenv("UPLOAD_TTL")
	if value == "" {
		return 2 * time.Hour
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 2 * time.Hour
	}
	return parsed
}

func uploadBaseDir() string {
	if value := os.Getenv("UPLOAD_DIR"); value != "" {
		return value
	}
	return "uploads"
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

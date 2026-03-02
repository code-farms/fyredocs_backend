package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"cleanup-worker/config"
	"cleanup-worker/database"
	"cleanup-worker/redisstore"
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

	for {
		runCleanup(context.Background())
		<-ticker.C
	}
}

func runCleanup(ctx context.Context) {
	cleanupExpiredJobs(ctx)
	cleanupUploadState(ctx)
	requeueStaleJobs(ctx, queueName("convert-from-pdf"))
	requeueStaleJobs(ctx, queueName("convert-to-pdf"))
}

func cleanupExpiredJobs(ctx context.Context) {
	var jobs []database.ProcessingJob
	now := time.Now().UTC()
	query := database.DB.Where("user_id IS NULL AND expires_at IS NOT NULL AND expires_at <= ?", now)
	if err := query.Find(&jobs).Error; err != nil {
		log.Printf("cleanup jobs query failed: %v", err)
		return
	}

	for _, job := range jobs {
		var files []database.FileMetadata
		_ = database.DB.Where("job_id = ?", job.ID).Find(&files).Error
		for _, file := range files {
			_ = os.Remove(file.Path)
		}
		_ = database.DB.Where("job_id = ?", job.ID).Delete(&database.FileMetadata{}).Error
		if err := database.DB.Delete(&job).Error; err != nil {
			log.Printf("failed to delete job %s: %v", job.ID, err)
		}
	}
}

func cleanupUploadState(ctx context.Context) {
	if redisstore.Client == nil {
		return
	}
	iter := redisstore.Client.Scan(ctx, 0, "upload:*", 0).Iterator()
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
			redisstore.Client.Del(ctx, key, key+":chunks")
			uploadID := strings.TrimPrefix(key, "upload:")
			// Validate that uploadID is a UUID to prevent path traversal.
			if _, uuidErr := uuid.Parse(uploadID); uuidErr == nil {
				_ = os.RemoveAll(filepath.Join(uploadBaseDir(), "tmp", uploadID))
			}
		}
	}
}

func requeueStaleJobs(ctx context.Context, queue string) {
	if redisstore.Client == nil {
		return
	}
	processingList := queue + ":processing"
	items, err := redisstore.Client.LRange(ctx, processingList, 0, -1).Result()
	if err != nil {
		return
	}
	for _, item := range items {
		var payload JobPayload
		if err := json.Unmarshal([]byte(item), &payload); err != nil {
			redisstore.Client.LRem(ctx, processingList, 1, item)
			continue
		}
		if payload.JobID == "" {
			redisstore.Client.LRem(ctx, processingList, 1, item)
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
			redisstore.Client.LRem(ctx, processingList, 1, item)
			continue
		}

		redisstore.Client.LRem(ctx, processingList, 1, item)
		newPayload, _ := json.Marshal(payload)
		redisstore.Client.LPush(ctx, queue, newPayload)
		redisstore.Client.Del(ctx, key)
	}
}

func markFailed(jobID string, reason string) {
	parsed, err := uuid.Parse(jobID)
	if err != nil {
		return
	}
	updates := map[string]interface{}{
		"status":         "failed",
		"progress":       "0",
		"failure_reason": reason,
	}
	database.DB.Model(&database.ProcessingJob{}).Where("id = ?", parsed).Updates(updates)
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

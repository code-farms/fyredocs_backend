package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"esydocs/shared/config"
	"esydocs/shared/logger"
	"esydocs/shared/redisstore"
	"esydocs/shared/telemetry"

	"cleanup-worker/internal/models"
)

func main() {
	config.LoadConfig()
	logger.Init("cleanup-worker", os.Getenv("LOG_MODE"))
	shutdownTracer := telemetry.Init("cleanup-worker")
	defer shutdownTracer(context.Background())
	models.Connect()
	models.Migrate()
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
	// Acquire distributed lock to prevent concurrent cleanup runs
	lockKey := "cleanup-worker:lock"
	lockTTL := 10 * time.Minute
	ok, err := redisstore.Client.SetNX(ctx, lockKey, "1", lockTTL).Result()
	if err != nil {
		slog.Error("failed to acquire cleanup lock", "error", err)
		return
	}
	if !ok {
		slog.Debug("cleanup lock held by another instance, skipping")
		return
	}
	defer redisstore.Client.Del(ctx, lockKey)

	cleanupExpiredJobs(ctx)
	cleanupUploadState(ctx)
}

func cleanupExpiredJobs(ctx context.Context) {
	now := time.Now().UTC()
	for {
		var jobs []models.ProcessingJob
		query := models.DB.Where("user_id IS NULL AND expires_at IS NOT NULL AND expires_at <= ?", now).Limit(100)
		if err := query.Find(&jobs).Error; err != nil {
			slog.Error("cleanup jobs query failed", "error", err)
			return
		}
		if len(jobs) == 0 {
			return
		}

		for _, job := range jobs {
			var files []models.FileMetadata
			if err := models.DB.Where("job_id = ?", job.ID).Find(&files).Error; err != nil {
				slog.Error("failed to find files for job", "jobId", job.ID, "error", err)
			}
			for _, file := range files {
				if err := os.Remove(file.Path); err != nil && !os.IsNotExist(err) {
					slog.Warn("failed to remove file", "path", file.Path, "error", err)
				}
			}
			if err := models.DB.Where("job_id = ?", job.ID).Delete(&models.FileMetadata{}).Error; err != nil {
				slog.Error("failed to delete file metadata", "jobId", job.ID, "error", err)
			}
			if err := models.DB.Delete(&job).Error; err != nil {
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

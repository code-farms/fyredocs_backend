package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"esydocs/shared/config"
	"esydocs/shared/logger"
	"esydocs/shared/metrics"
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start HTTP server for health checks and metrics
	r := gin.New()
	r.Use(telemetry.GinTraceMiddleware("cleanup-worker"))
	r.Use(metrics.GinMetricsMiddleware())
	r.Use(logger.GinRequestID())
	r.Use(logger.GinRequestLogger())
	r.Use(gin.Recovery())
	r.GET("/metrics", metrics.MetricsHandler())
	if err := r.SetTrustedProxies(config.TrustedProxies()); err != nil {
		slog.Error("failed to set trusted proxies", "error", err)
		os.Exit(1)
	}

	r.GET("/healthz", func(c *gin.Context) {
		hctx, hcancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer hcancel()
		if err := redisstore.Client.Ping(hctx).Err(); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "redis": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "healthy"})
	})

	r.GET("/readyz", func(c *gin.Context) {
		checks := gin.H{}
		ready := true

		hctx, hcancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer hcancel()

		if err := redisstore.Client.Ping(hctx).Err(); err != nil {
			checks["redis"] = err.Error()
			ready = false
		} else {
			checks["redis"] = "ok"
		}

		if err := models.DB.Exec("SELECT 1").Error; err != nil {
			checks["postgres"] = err.Error()
			ready = false
		} else {
			checks["postgres"] = "ok"
		}

		if !ready {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready", "checks": checks})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready", "checks": checks})
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8088"
	}

	srv := &http.Server{Addr: ":" + port, Handler: r}
	go func() {
		slog.Info("cleanup-worker HTTP server listening", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server failed", "error", err)
			os.Exit(1)
		}
	}()

	// Start cleanup loop
	interval := cleanupInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	slog.Info("cleanup-worker started", "interval", interval)

	go func() {
		for {
			runCleanup(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	// Wait for shutdown signal
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	slog.Info("shutting down")
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown error", "error", err)
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
	cleanupOrphanedDirs(ctx)
	backfillExpiry(ctx)
}

func cleanupExpiredJobs(ctx context.Context) {
	now := time.Now().UTC()
	for {
		var jobs []models.ProcessingJob
		query := models.DB.Where("expires_at IS NOT NULL AND expires_at <= ?", now).Limit(100)
		if err := query.Find(&jobs).Error; err != nil {
			slog.Error("cleanup jobs query failed", "error", err)
			return
		}
		if len(jobs) == 0 {
			return
		}

		// Batch-fetch all file metadata for this batch of jobs (fixes N+1)
		jobIDs := make([]uuid.UUID, len(jobs))
		for i, j := range jobs {
			jobIDs[i] = j.ID
		}
		var allFiles []models.FileMetadata
		if err := models.DB.Where("job_id IN ?", jobIDs).Find(&allFiles).Error; err != nil {
			slog.Error("failed to batch-fetch files for cleanup", "error", err)
		}

		// Group files by job ID
		filesByJob := make(map[uuid.UUID][]models.FileMetadata, len(jobs))
		for _, f := range allFiles {
			filesByJob[f.JobID] = append(filesByJob[f.JobID], f)
		}

		for _, job := range jobs {
			for _, file := range filesByJob[job.ID] {
				if err := os.Remove(file.Path); err != nil && !os.IsNotExist(err) {
					slog.Warn("failed to remove file", "path", file.Path, "error", err)
				}
			}
			// Remove the now-empty job directories
			jobID := job.ID.String()
			uploadDir := filepath.Join(uploadBaseDir(), jobID)
			os.Remove(uploadDir)
			outputDir := filepath.Join(outputBaseDir(), jobID)
			os.Remove(outputDir)
		}

		// Batch-delete file metadata and jobs
		if err := models.DB.Where("job_id IN ?", jobIDs).Delete(&models.FileMetadata{}).Error; err != nil {
			slog.Error("failed to batch-delete file metadata", "error", err)
		}
		if err := models.DB.Where("id IN ?", jobIDs).Delete(&models.ProcessingJob{}).Error; err != nil {
			slog.Error("failed to batch-delete jobs", "error", err)
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

var outputFileJobIDRegexp = regexp.MustCompile(`^[a-z]+_([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})_`)

func cleanupOrphanedDirs(ctx context.Context) {
	// Phase 1: Remove upload directories with no matching DB record
	uploadsDir := uploadBaseDir()
	entries, err := os.ReadDir(uploadsDir)
	if err != nil {
		slog.Error("failed to read uploads directory", "error", err)
		return
	}

	// Collect all candidate UUIDs from upload dirs
	candidateUploadIDs := make([]uuid.UUID, 0, len(entries))
	candidateUploadNames := make(map[uuid.UUID]string, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "tmp" {
			continue
		}
		parsed, err := uuid.Parse(entry.Name())
		if err != nil {
			continue
		}
		candidateUploadIDs = append(candidateUploadIDs, parsed)
		candidateUploadNames[parsed] = entry.Name()
	}

	if len(candidateUploadIDs) > 0 {
		// Single query to find which job IDs exist
		var existingIDs []uuid.UUID
		if err := models.DB.Model(&models.ProcessingJob{}).Where("id IN ?", candidateUploadIDs).Pluck("id", &existingIDs).Error; err != nil {
			slog.Error("failed to batch-check job existence for uploads", "error", err)
		} else {
			existingSet := make(map[uuid.UUID]struct{}, len(existingIDs))
			for _, id := range existingIDs {
				existingSet[id] = struct{}{}
			}
			for _, id := range candidateUploadIDs {
				if _, exists := existingSet[id]; !exists {
					// Check if this is an active upload not yet consumed by a job
					if redisstore.Client != nil {
						uploadKey := "upload:" + id.String()
						if exists, err := redisstore.Client.Exists(ctx, uploadKey).Result(); err == nil && exists > 0 {
							continue
						}
					}
					dirPath := filepath.Join(uploadsDir, candidateUploadNames[id])
					if err := os.RemoveAll(dirPath); err != nil {
						slog.Warn("failed to remove orphaned upload dir", "path", dirPath, "error", err)
					} else {
						slog.Info("removed orphaned upload dir", "jobId", id)
					}
				}
			}
		}
	}

	// Phase 2: Remove output files with no matching DB record
	outputsDir := outputBaseDir()
	outputEntries, err := os.ReadDir(outputsDir)
	if err != nil {
		slog.Error("failed to read outputs directory", "error", err)
		return
	}

	// Collect all candidate UUIDs from output files
	type outputCandidate struct {
		jobID    uuid.UUID
		fileName string
	}
	var outputCandidates []outputCandidate
	candidateOutputIDs := make([]uuid.UUID, 0)
	for _, entry := range outputEntries {
		if entry.IsDir() || entry.Name() == ".gitkeep" {
			continue
		}
		matches := outputFileJobIDRegexp.FindStringSubmatch(entry.Name())
		if len(matches) < 2 {
			continue
		}
		parsed, err := uuid.Parse(matches[1])
		if err != nil {
			continue
		}
		outputCandidates = append(outputCandidates, outputCandidate{jobID: parsed, fileName: entry.Name()})
		candidateOutputIDs = append(candidateOutputIDs, parsed)
	}

	if len(candidateOutputIDs) > 0 {
		var existingIDs []uuid.UUID
		if err := models.DB.Model(&models.ProcessingJob{}).Where("id IN ?", candidateOutputIDs).Pluck("id", &existingIDs).Error; err != nil {
			slog.Error("failed to batch-check job existence for outputs", "error", err)
		} else {
			existingSet := make(map[uuid.UUID]struct{}, len(existingIDs))
			for _, id := range existingIDs {
				existingSet[id] = struct{}{}
			}
			for _, oc := range outputCandidates {
				if _, exists := existingSet[oc.jobID]; !exists {
					filePath := filepath.Join(outputsDir, oc.fileName)
					if err := os.Remove(filePath); err != nil {
						slog.Warn("failed to remove orphaned output file", "path", filePath, "error", err)
					} else {
						slog.Info("removed orphaned output file", "jobId", oc.jobID, "file", oc.fileName)
					}
				}
			}
		}
	}
}

func backfillExpiry(ctx context.Context) {
	ttl := freeJobTTL()
	result := models.DB.Model(&models.ProcessingJob{}).
		Where("user_id IS NOT NULL AND expires_at IS NULL").
		Update("expires_at", gorm.Expr("created_at + interval '1 second' * ?", int(ttl.Seconds())))
	if result.Error != nil {
		slog.Error("backfill expiry failed", "error", result.Error)
	} else if result.RowsAffected > 0 {
		slog.Info("backfilled expires_at for old jobs", "count", result.RowsAffected)
	}
}

func freeJobTTL() time.Duration {
	value := os.Getenv("FREE_JOB_TTL")
	if value == "" {
		return 24 * time.Hour
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 24 * time.Hour
	}
	return parsed
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

func outputBaseDir() string {
	if value := os.Getenv("OUTPUT_DIR"); value != "" {
		return value
	}
	return "outputs"
}


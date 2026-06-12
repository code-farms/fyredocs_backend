package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"fyredocs/shared/config"
	"fyredocs/shared/logger"
	"fyredocs/shared/metrics"
	"fyredocs/shared/redisstore"
	"fyredocs/shared/storage"
	"fyredocs/shared/telemetry"

	"cleanup-worker/internal/models"
)

// objectStore is the narrow slice of fyredocs/shared/storage.Client the
// cleanup worker needs. Declared locally so tests can substitute a fake.
type objectStore interface {
	BucketUploads() string
	BucketOutputs() string
	RemoveObject(ctx context.Context, bucket, key string) error
	AbortMultipart(ctx context.Context, bucket, key, s3UploadID string) error
	ListIncompleteUploads(ctx context.Context, bucket string, olderThan time.Duration) ([]storage.IncompleteUpload, error)
}

func main() {
	config.LoadConfig()
	logger.Init("cleanup-worker", os.Getenv("LOG_MODE"))
	shutdownTracer := telemetry.Init("cleanup-worker")
	defer shutdownTracer(context.Background())
	models.Connect()
	models.Migrate()
	redisstore.Connect()

	// Object storage is a hard dependency: every cleanup phase that touches
	// file data goes through MinIO/S3 now, so fail fast if it is missing.
	store, err := storage.NewFromEnv()
	if err != nil {
		slog.Error("object storage init failed", "error", err)
		os.Exit(1)
	}

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
			runCleanup(ctx, store)
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

func runCleanup(ctx context.Context, store objectStore) {
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

	cleanupExpiredJobs(ctx, store)
	cleanupUploadState(ctx, store)
	abortStaleMultipartUploads(ctx, store)
	backfillExpiry(ctx)
}

// bucketFor maps a FileMetadata.Kind to the bucket that holds the object.
// Kind "input" lives in the uploads bucket; everything else ("output") in
// the outputs bucket.
func bucketFor(store objectStore, kind string) string {
	if kind == "input" {
		return store.BucketUploads()
	}
	return store.BucketOutputs()
}

// removeJobObjects deletes every object referenced by files from object
// storage. Rows whose Path starts with "/" are legacy local-filesystem paths
// from before the MinIO migration — they are skipped (logged once per call)
// and left for the one-off migration script (scripts/migrate-files-to-minio.sh).
func removeJobObjects(ctx context.Context, store objectStore, files []models.FileMetadata) {
	legacyLogged := false
	for _, file := range files {
		if strings.HasPrefix(file.Path, "/") {
			if !legacyLogged {
				slog.Warn("skipping legacy filesystem path(s); run scripts/migrate-files-to-minio.sh", "path", file.Path, "jobId", file.JobID)
				legacyLogged = true
			}
			continue
		}
		bucket := bucketFor(store, file.Kind)
		if err := store.RemoveObject(ctx, bucket, file.Path); err != nil {
			slog.Warn("failed to remove object", "bucket", bucket, "key", file.Path, "error", err)
		}
	}
}

func cleanupExpiredJobs(ctx context.Context, store objectStore) {
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

		// Remove every referenced object from MinIO. RemoveObject treats a
		// missing object as success, so this is idempotent across retries.
		removeJobObjects(ctx, store, allFiles)

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

// uploadObjectConsumed reports whether an uploads-bucket key is referenced by
// a file_metadata row, i.e. a job has consumed the upload. On query error it
// returns true so we never delete an object we cannot prove is unreferenced.
func uploadObjectConsumed(key string) bool {
	var count int64
	if err := models.DB.Model(&models.FileMetadata{}).Where("path = ?", key).Count(&count).Error; err != nil {
		slog.Warn("failed to check upload consumption, keeping object", "key", key, "error", err)
		return true
	}
	return count > 0
}

// reapExpiredUploadObjects releases the object-storage resources behind an
// expired upload session: it aborts the multipart upload (if one was started)
// and removes the assembled object unless a job already consumed it
// (consumed keys are referenced by file_metadata and cleaned with the job).
func reapExpiredUploadObjects(ctx context.Context, store objectStore, objectKey, s3UploadID string, consumed func(key string) bool) {
	if objectKey == "" {
		return
	}
	uploads := store.BucketUploads()
	if s3UploadID != "" {
		if err := store.AbortMultipart(ctx, uploads, objectKey, s3UploadID); err != nil {
			slog.Warn("failed to abort multipart upload", "key", objectKey, "s3UploadId", s3UploadID, "error", err)
		}
	}
	if consumed(objectKey) {
		return
	}
	if err := store.RemoveObject(ctx, uploads, objectKey); err != nil {
		slog.Warn("failed to remove expired upload object", "key", objectKey, "error", err)
	}
}

func cleanupUploadState(ctx context.Context, store objectStore) {
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
		state, err := redisstore.Client.HGetAll(ctx, key).Result()
		if err != nil {
			slog.Warn("cleanup: read upload state failed", "key", key, "err", err)
			continue
		}
		createdAt := state["createdAt"]
		if createdAt == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			slog.Warn("cleanup: parse createdAt failed", "key", key, "createdAt", createdAt, "err", err)
			continue
		}
		if time.Since(parsed) > ttl {
			if err := redisstore.Client.Del(ctx, key, key+":chunks").Err(); err != nil {
				slog.Warn("failed to delete upload state", "key", key, "error", err)
			}
			// Release object-storage resources tied to the expired session:
			// abort any in-progress multipart upload and remove the object
			// if no job consumed it.
			reapExpiredUploadObjects(ctx, store, state["key"], state["s3UploadId"], uploadObjectConsumed)
		}
	}
	if err := iter.Err(); err != nil {
		slog.Error("SCAN iterator error", "error", err)
	}
}

// staleMultipartAge is how old an incomplete multipart upload must be before
// the worker aborts it. The bucket lifecycle rule (1 day) is the backstop;
// this catches uploads whose Redis session vanished without an abort.
const staleMultipartAge = 24 * time.Hour

// abortStaleMultipartUploads aborts multipart uploads in the uploads bucket
// that were initiated more than staleMultipartAge ago and never completed.
func abortStaleMultipartUploads(ctx context.Context, store objectStore) {
	uploads := store.BucketUploads()
	stale, err := store.ListIncompleteUploads(ctx, uploads, staleMultipartAge)
	if err != nil {
		slog.Error("failed to list incomplete multipart uploads", "bucket", uploads, "error", err)
		return
	}
	for _, u := range stale {
		if err := store.AbortMultipart(ctx, uploads, u.Key, u.UploadID); err != nil {
			slog.Warn("failed to abort stale multipart upload", "key", u.Key, "s3UploadId", u.UploadID, "error", err)
			continue
		}
		slog.Info("aborted stale multipart upload", "key", u.Key, "initiated", u.Initiated)
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

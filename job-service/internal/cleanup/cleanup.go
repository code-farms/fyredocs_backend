// Package cleanup contains the TTL sweep that reaps expired jobs, upload
// sessions, and orphaned object-storage artifacts. It runs as a separate
// binary (cmd/cleanup) but lives inside job-service because everything it
// touches — processing_jobs, file_metadata, upload:* Redis state, the uploads
// and outputs buckets — is job-service-owned data.
package cleanup

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"fyredocs/shared/config"
	"fyredocs/shared/redisstore"
	"fyredocs/shared/storage"

	"job-service/internal/models"
)

// ObjectStore is the narrow slice of fyredocs/shared/storage.Client the
// cleanup sweep needs. Declared locally so tests can substitute a fake.
type ObjectStore interface {
	BucketUploads() string
	BucketOutputs() string
	RemoveObject(ctx context.Context, bucket, key string) error
	AbortMultipart(ctx context.Context, bucket, key, s3UploadID string) error
	ListIncompleteUploads(ctx context.Context, bucket string, olderThan time.Duration) ([]storage.IncompleteUpload, error)
}

// cleanupLockKey names the distributed lock serialising sweeps. Every replica runs
// the sweep timer, so without it N replicas would each try to delete the same
// expired objects concurrently.
const cleanupLockKey = "cleanup-worker:lock"

// cleanupLockReleaseScript releases the lock only if we still own it
// (finding B3 — docs/developer/architecture/backend-hardening.md section 3.20).
// Comparing the stored token to ours before deleting stops a replica whose sweep
// outran the lock TTL from deleting a DIFFERENT replica's freshly-acquired lock,
// which would otherwise permit concurrent sweeps.
var cleanupLockReleaseScript = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
	return redis.call("del", KEYS[1])
end
return 0
`)

// RunSweep executes one full cleanup pass. A Redis lock guards against concurrent
// sweeps from multiple cleanup replicas: each holder writes a unique token and
// releases only if the token still matches (compare-and-delete), so a slow sweep
// that outlives the TTL can never delete another holder's lock.
func RunSweep(ctx context.Context, store ObjectStore) {
	lockTTL := 10 * time.Minute
	token := uuid.NewString()
	ok, err := redisstore.Client.SetNX(ctx, cleanupLockKey, token, lockTTL).Result()
	if err != nil {
		slog.Error("failed to acquire cleanup lock", "error", err)
		return
	}
	if !ok {
		slog.Debug("cleanup lock held by another instance, skipping")
		return
	}
	// Release only if we still own the lock (see releaseCleanupLock).
	defer releaseCleanupLock(token)

	cleanupExpiredJobs(ctx, store)
	cleanupExpiredShares(ctx, store)
	cleanupUploadState(ctx, store)
	abortStaleMultipartUploads(ctx, store)
	backfillExpiry(ctx)
}

// cleanupExpiredShares reaps share links past expiry or revoked, along with
// the objects they own under shares/ in the outputs bucket. Same ordering
// discipline as jobs: objects BEFORE rows, so a mid-sweep crash leaves a row
// the next sweep retries idempotently (missing object is not an error) rather
// than an unreferenced object billed forever.
func cleanupExpiredShares(ctx context.Context, store ObjectStore) {
	now := time.Now().UTC()
	for {
		var shares []models.Share
		if err := models.DB.Where("expires_at <= ? OR revoked_at IS NOT NULL", now).
			Limit(100).Find(&shares).Error; err != nil {
			slog.Error("cleanup shares query failed", "error", err)
			return
		}
		if len(shares) == 0 {
			return
		}

		ids := make([]uuid.UUID, len(shares))
		for i, s := range shares {
			ids[i] = s.ID
			if err := store.RemoveObject(ctx, store.BucketOutputs(), s.ObjectKey); err != nil {
				slog.Warn("failed to remove share object", "key", s.ObjectKey, "error", err)
			}
		}
		if err := models.DB.Where("id IN ?", ids).Delete(&models.Share{}).Error; err != nil {
			slog.Error("failed to batch-delete shares", "error", err)
			return
		}
		slog.Info("cleaned up expired shares", "count", len(shares))
	}
}

// releaseCleanupLock deletes the sweep lock only when the stored token still
// matches ours (compare-and-delete). It runs on a fresh background context
// because the sweep's context may already be cancelled. A mismatch is a no-op —
// another replica now owns the lock and must not have it deleted.
func releaseCleanupLock(token string) {
	relCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := cleanupLockReleaseScript.Run(relCtx, redisstore.Client, []string{cleanupLockKey}, token).Err(); err != nil {
		slog.Warn("failed to release cleanup lock", "error", err)
	}
}

// bucketFor maps a FileMetadata.Kind to the bucket that holds the object.
// Kind "input" lives in the uploads bucket; everything else ("output") in
// the outputs bucket.
func bucketFor(store ObjectStore, kind string) string {
	if kind == "input" {
		return store.BucketUploads()
	}
	return store.BucketOutputs()
}

// removeJobObjects deletes every object referenced by files from object
// storage. Rows whose Path starts with "/" are legacy local-filesystem paths
// from before the MinIO migration — they are skipped (logged once per call)
// and left for the one-off migration script (scripts/migrate-files-to-minio.sh).
func removeJobObjects(ctx context.Context, store ObjectStore, files []models.FileMetadata) {
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

// cleanupExpiredJobs deletes jobs past their retention expiry along with their
// stored objects.
//
// Objects are deleted BEFORE the database row. Removing the row first would orphan
// the object with nothing left referencing it — unreachable but still billed,
// forever. This ordering means a crash mid-sweep leaves a row whose objects are
// already gone, which the next sweep handles idempotently since a missing object
// is not an error.
//
// Rows whose Path is an absolute filesystem path predate the object-storage
// migration and are skipped rather than misread as object keys.
func cleanupExpiredJobs(ctx context.Context, store ObjectStore) {
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
func reapExpiredUploadObjects(ctx context.Context, store ObjectStore, objectKey, s3UploadID string, consumed func(key string) bool) {
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

// uploadReapAge is the age past which a still-present upload session is
// considered abandoned and its storage reaped. Redis expires the session hash
// after config.UploadTTL() on its own; the sweep only sees sessions whose TTL
// was refreshed or extended, so it waits a conservative 2× the session TTL
// before touching anything (this preserves the old cleanup-worker behavior of
// reaping later than the session lifetime, without a divergent constant).
func uploadReapAge() time.Duration {
	return 2 * config.UploadTTL()
}

// cleanupUploadState aborts S3 multipart uploads whose Redis session has expired.
//
// Redis expiry alone is insufficient: the session key vanishing does not tell S3
// to discard parts already uploaded against it, and those parts are billed until
// explicitly aborted. This is the only thing that reclaims them.
func cleanupUploadState(ctx context.Context, store ObjectStore) {
	if redisstore.Client == nil {
		return
	}
	iter := redisstore.Client.Scan(ctx, 0, "upload:*", 100).Iterator()
	reapAge := uploadReapAge()
	for iter.Next(ctx) {
		key := iter.Val()
		if strings.Contains(key, ":chunks") {
			continue
		}
		state, err := redisstore.Client.HGetAll(ctx, key).Result()
		if err != nil {
			slog.Warn("cleanup: read upload state failed", "key", key, "error", err)
			continue
		}
		createdAt := state["createdAt"]
		if createdAt == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			slog.Warn("cleanup: parse createdAt failed", "key", key, "createdAt", createdAt, "error", err)
			continue
		}
		if time.Since(parsed) > reapAge {
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

// abortStaleMultipartUploads aborts multipart uploads in the uploads bucket
// that were initiated more than config.StaleMultipartAge() ago and never
// completed. There is no bucket lifecycle rule, so this fully owns aborting
// incomplete multipart uploads whose Redis session vanished without an abort.
func abortStaleMultipartUploads(ctx context.Context, store ObjectStore) {
	uploads := store.BucketUploads()
	stale, err := store.ListIncompleteUploads(ctx, uploads, config.StaleMultipartAge())
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

// backfillExpiry assigns a retention expiry to rows predating per-plan TTLs.
//
// A NULL expires_at is invisible to the expiry sweep, so without this such a row
// lingers indefinitely. It stays part of the recurring sweep rather than being a
// one-off migration so it also covers rows restored from an old backup.
func backfillExpiry(ctx context.Context) {
	ttl := config.FreeJobTTL()
	result := models.DB.Model(&models.ProcessingJob{}).
		Where("user_id IS NOT NULL AND expires_at IS NULL").
		Update("expires_at", gorm.Expr("created_at + interval '1 second' * ?", int(ttl.Seconds())))
	if result.Error != nil {
		slog.Error("backfill expiry failed", "error", result.Error)
	} else if result.RowsAffected > 0 {
		slog.Info("backfilled expires_at for old jobs", "count", result.RowsAffected)
	}
}

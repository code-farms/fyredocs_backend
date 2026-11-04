package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"document-service/internal/models"
)

const (
	// hashTimeout bounds one object read (documents are ≤ a few hundred MB on
	// the free path; MinIO streams them well inside this).
	hashTimeout = 2 * time.Minute
	// hashConcurrency caps simultaneous background hashes so a burst of job
	// completions can't open hundreds of object streams at once.
	hashConcurrency = 4
)

// hashSlots is the concurrency semaphore for HashDocumentAsync.
var hashSlots = make(chan struct{}, hashConcurrency)

// sha256Object streams one stored object through SHA-256 and returns lowercase
// hex. The object is never buffered whole.
func sha256Object(ctx context.Context, store ObjectStore, bucket, key string) (string, error) {
	r, err := store.GetObject(ctx, bucket, key)
	if err != nil {
		return "", err
	}
	defer r.Close()
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// HashDocumentAsync computes and stores a document's content SHA-256 in the
// background. Called after a document gets bytes (CreateDocument with a
// storagePath, the job-finalize subscriber) and lazily by GetDuplicates.
//
// Best-effort and fail-silent by contract: a missing store, an empty path or a
// read error leaves content_sha256 NULL, which only means "no exact-duplicate
// match yet" — document writes must never depend on hashing.
func HashDocumentAsync(id uuid.UUID, storagePath string) {
	store := objStore
	if store == nil || storagePath == "" || models.DB == nil {
		return
	}
	go func() {
		hashSlots <- struct{}{}
		defer func() { <-hashSlots }()
		ctx, cancel := context.WithTimeout(context.Background(), hashTimeout)
		defer cancel()
		sum, err := sha256Object(ctx, store, store.BucketOutputs(), storagePath)
		if err != nil {
			slog.Warn("document hash failed", "docId", id, "error", err)
			return
		}
		// UpdateColumn: a derived fingerprint is not a user edit, so it must not
		// bump updated_at — delta-sync clients would re-pull every hashed row.
		if err := models.DB.WithContext(ctx).Model(&models.Document{}).Where("id = ?", id).
			UpdateColumn("content_sha256", sum).Error; err != nil {
			slog.Warn("document hash save failed", "docId", id, "error", err)
		}
	}()
}

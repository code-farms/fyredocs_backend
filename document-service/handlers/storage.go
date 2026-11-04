package handlers

import (
	"context"
	"io"
	"net/url"
	"time"
)

// ObjectStore is the narrow slice of fyredocs/shared/storage.Client this
// service uses (Phase 9): presigning document downloads and deleting backup
// objects on purge. shared/storage is a utility package (its charter:
// "carries NO business logic") — importing it here does not breach the
// no-shared-business-logic rule; the upload PROTOCOL stays in job-service.
type ObjectStore interface {
	BucketOutputs() string
	PresignGet(ctx context.Context, bucket, key string, expiry time.Duration, reqParams url.Values) (string, error)
	RemoveObject(ctx context.Context, bucket, key string) error
	// GetObject streams an object — used to compute content SHA-256 (hashing.go).
	GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, error)
}

// objStore is injected at boot via SetObjectStore. A nil store degrades the
// download endpoint to 503 and purge to row-only (object leak logged) —
// document CRUD must never depend on storage availability.
var objStore ObjectStore

// SetObjectStore injects the S3 client used by download/purge. Called from
// main() at boot; tests inject a fake.
func SetObjectStore(s ObjectStore) {
	objStore = s
}

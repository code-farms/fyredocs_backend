package handlers

import (
	"context"
	"io"
	"net/url"
	"time"

	"fyredocs/shared/storage"
)

// ObjectStore is the narrow slice of fyredocs/shared/storage.Client that the
// job-service handlers use. Declaring it locally (instead of depending on the
// concrete *storage.Client) keeps the handlers testable with an in-memory
// fake and documents exactly which storage operations this service performs.
type ObjectStore interface {
	BucketUploads() string
	BucketOutputs() string
	PutObject(ctx context.Context, bucket, key string, r io.Reader, size int64, contentType string) error
	GetObjectRange(ctx context.Context, bucket, key string, offset, length int64) ([]byte, error)
	StatObject(ctx context.Context, bucket, key string) (storage.ObjectInfo, error)
	RemoveObject(ctx context.Context, bucket, key string) error
	PresignGet(ctx context.Context, bucket, key string, expiry time.Duration, reqParams url.Values) (string, error)
	CreateMultipart(ctx context.Context, bucket, key, contentType string) (string, error)
	PresignUploadPart(ctx context.Context, bucket, key, s3UploadID string, partNumber int, expiry time.Duration) (string, error)
	CompleteMultipart(ctx context.Context, bucket, key, s3UploadID string, parts []storage.CompletedPart) error
	AbortMultipart(ctx context.Context, bucket, key, s3UploadID string) error
}

// objStore is injected once at boot via SetObjectStore. Handlers treat a nil
// store as a 500 (service misconfigured) rather than panicking.
var objStore ObjectStore

// SetObjectStore injects the S3 client used by upload/job handlers.
// Called from main() at boot; tests inject a fake.
func SetObjectStore(s ObjectStore) {
	objStore = s
}

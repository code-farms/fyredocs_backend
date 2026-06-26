package worker

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"fyredocs/shared/storage"
)

// Storage is the narrow object-storage surface the worker needs. It is
// satisfied by *storage.Client (fyredocs/shared/storage) in production and by
// lightweight fakes in tests.
type Storage interface {
	DownloadToFile(ctx context.Context, bucket, key, localPath string) error
	UploadFromFile(ctx context.Context, bucket, key, localPath, contentType string) (int64, error)
	GetObjectSize(ctx context.Context, bucket, key string) (int64, error)
	StatObject(ctx context.Context, bucket, key string) (storage.ObjectInfo, error)
	CopyObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string) error
	BucketUploads() string
	BucketOutputs() string
}

// recoverableError marks an error as transient (network/storage) so that
// handleFailure NAKs the message for redelivery instead of failing the job
// permanently.
type recoverableError struct{ err error }

func (e *recoverableError) Error() string { return e.err.Error() }
func (e *recoverableError) Unwrap() error { return e.err }

// markRecoverable wraps err so that isRecoverable reports true.
func markRecoverable(err error) error {
	if err == nil {
		return nil
	}
	return &recoverableError{err: err}
}

// fetchInputs downloads every object key from the uploads bucket into
// <scratch>/in/ and returns the local file paths in input order.
func fetchInputs(ctx context.Context, store Storage, scratch string, keys []string) ([]string, error) {
	localPaths := make([]string, 0, len(keys))
	for _, key := range keys {
		localPath := filepath.Join(scratch, "in", path.Base(key))
		if err := store.DownloadToFile(ctx, store.BucketUploads(), key, localPath); err != nil {
			return nil, fmt.Errorf("fetch input %q: %w", key, err)
		}
		localPaths = append(localPaths, localPath)
	}
	return localPaths, nil
}

// storeOutput uploads localPath to the outputs bucket under
// jobs/{jobID}/{basename} and returns the object key plus the uploaded size.
func storeOutput(ctx context.Context, store Storage, jobID, localPath string) (string, int64, error) {
	key := "jobs/" + jobID + "/" + filepath.Base(localPath)
	size, err := store.UploadFromFile(ctx, store.BucketOutputs(), key, localPath, contentTypeFor(localPath))
	if err != nil {
		return "", 0, fmt.Errorf("store output %q: %w", key, err)
	}
	return key, size, nil
}

// contentTypeFor maps an output file extension to its MIME type, defaulting
// to application/octet-stream for unknown extensions.
func contentTypeFor(p string) string {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".pdf":
		return "application/pdf"
	case ".zip":
		return "application/zip"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case ".odt":
		return "application/vnd.oasis.opendocument.text"
	case ".ods":
		return "application/vnd.oasis.opendocument.spreadsheet"
	case ".odp":
		return "application/vnd.oasis.opendocument.presentation"
	case ".html", ".htm":
		return "text/html"
	case ".txt":
		return "text/plain"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".tif", ".tiff":
		return "image/tiff"
	default:
		return "application/octet-stream"
	}
}

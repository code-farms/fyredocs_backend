package storage

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/minio/minio-go/v7"
)

// CreateMultipart starts an S3 multipart upload and returns its S3 upload ID.
func (c *Client) CreateMultipart(ctx context.Context, bucket, key, contentType string) (string, error) {
	id, err := c.core.NewMultipartUpload(ctx, bucket, key, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return "", fmt.Errorf("storage: create multipart %s/%s: %w", bucket, key, err)
	}
	return id, nil
}

// PresignUploadPart returns a browser-reachable presigned URL for uploading
// one part of an in-progress multipart upload.
func (c *Client) PresignUploadPart(ctx context.Context, bucket, key, s3UploadID string, partNumber int, expiry time.Duration) (string, error) {
	params := url.Values{}
	params.Set("uploadId", s3UploadID)
	params.Set("partNumber", strconv.Itoa(partNumber))
	u, err := c.public.Presign(ctx, "PUT", bucket, key, expiry, params)
	if err != nil {
		return "", fmt.Errorf("storage: presign part %d of %s/%s: %w", partNumber, bucket, key, err)
	}
	return u.String(), nil
}

// CompleteMultipart finishes a multipart upload from the client-collected
// part ETags.
func (c *Client) CompleteMultipart(ctx context.Context, bucket, key, s3UploadID string, parts []CompletedPart) error {
	completed := make([]minio.CompletePart, 0, len(parts))
	for _, p := range parts {
		completed = append(completed, minio.CompletePart{PartNumber: p.PartNumber, ETag: p.ETag})
	}
	if _, err := c.core.CompleteMultipartUpload(ctx, bucket, key, s3UploadID, completed, minio.PutObjectOptions{}); err != nil {
		return fmt.Errorf("storage: complete multipart %s/%s: %w", bucket, key, err)
	}
	return nil
}

// AbortMultipart cancels an in-progress multipart upload, freeing its parts.
// Aborting an unknown upload is not an error.
func (c *Client) AbortMultipart(ctx context.Context, bucket, key, s3UploadID string) error {
	if err := c.core.AbortMultipartUpload(ctx, bucket, key, s3UploadID); err != nil {
		resp := minio.ToErrorResponse(unwrapAll(err))
		if resp.Code == "NoSuchUpload" || resp.StatusCode == 404 {
			return nil
		}
		return fmt.Errorf("storage: abort multipart %s/%s: %w", bucket, key, err)
	}
	return nil
}

// ListIncompleteUploads returns multipart uploads in bucket older than
// olderThan that were never completed or aborted.
func (c *Client) ListIncompleteUploads(ctx context.Context, bucket string, olderThan time.Duration) ([]IncompleteUpload, error) {
	cutoff := time.Now().Add(-olderThan)
	var stale []IncompleteUpload
	for upload := range c.mc.ListIncompleteUploads(ctx, bucket, "", true) {
		if upload.Err != nil {
			return stale, fmt.Errorf("storage: list incomplete uploads %s: %w", bucket, upload.Err)
		}
		if upload.Initiated.Before(cutoff) {
			stale = append(stale, IncompleteUpload{
				Key:       upload.Key,
				UploadID:  upload.UploadID,
				Initiated: upload.Initiated,
			})
		}
	}
	return stale, nil
}

package handlers

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"fyredocs/shared/config"
	"fyredocs/shared/redisstore"
	"fyredocs/shared/response"
	"fyredocs/shared/storage"
)

// Presigned multipart upload protocol.
//
// The browser never streams file bytes through the job-service anymore:
//  1. POST /api/uploads/init             → create S3 multipart upload, presign all part URLs
//  2. PUT  <presigned part URL>          → browser uploads each part directly to MinIO/S3
//  3. GET  /api/uploads/:id/parts        → re-presign part URLs (resume / expiry refresh)
//  4. POST /api/uploads/:id/complete     → finish the multipart upload, verify size
//  5. POST /api/<group>/:tool {uploadId} → job creation consumes the uploaded object
//
// Session state lives in the Redis hash upload:{uploadId} (TTL UPLOAD_TTL,
// default 30m) with fields: fileName, declaredSize, contentType, bucket, key,
// s3UploadId, partSize, totalParts, createdAt — plus size once completed.

const (
	// partURLExpiry is how long presigned part/init URLs stay valid.
	partURLExpiry = 30 * time.Minute
	// maxUploadParts caps the number of multipart parts per upload.
	maxUploadParts = 1000
	// minPartSizeBytes is the S3 minimum size for non-terminal parts (5 MiB).
	minPartSizeBytes = 5 * 1024 * 1024
	// defaultPartSizeMB is used when UPLOAD_PART_SIZE_MB is unset/invalid.
	defaultPartSizeMB = 8
)

type UploadInitRequest struct {
	FileName    string `json:"fileName"`
	FileSize    int64  `json:"fileSize"`
	ContentType string `json:"contentType"`
}

type uploadPartURL struct {
	PartNumber int    `json:"partNumber"`
	URL        string `json:"url"`
}

type UploadCompleteRequest struct {
	Parts []UploadCompletedPart `json:"parts"`
}

type UploadCompletedPart struct {
	PartNumber int    `json:"partNumber"`
	ETag       string `json:"etag"`
}

// InitUpload creates an S3 multipart upload and returns presigned URLs for
// every part. The plan size limit is enforced on the declared size BEFORE any
// URL is issued (and re-verified against the true object size on complete).
func InitUpload(c *gin.Context) {
	if objStore == nil {
		response.InternalError(c, "SERVER_ERROR", "File storage is unavailable. Please try again later.")
		return
	}

	var req UploadInitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Errorf(c, http.StatusBadRequest, "INVALID_INPUT", "Invalid upload request. Please try again.", err,
			"op", "bind_upload_init")
		return
	}
	fileName := sanitizeFileName(req.FileName)
	if fileName == "" || req.FileSize <= 0 {
		response.BadRequest(c, "INVALID_INPUT", "Missing file information. Please try again.")
		return
	}

	maxFileSizeMB := planMaxFileSizeMB(c)
	if req.FileSize > int64(maxFileSizeMB)*1024*1024 {
		response.Err(c, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE",
			fmt.Sprintf("File size exceeds the %dMB limit for your plan", maxFileSizeMB))
		return
	}

	partSize := uploadPartSize()
	totalParts := int(math.Ceil(float64(req.FileSize) / float64(partSize)))
	if totalParts > maxUploadParts {
		response.BadRequest(c, "INVALID_INPUT", "File is too large to upload in one session. Please upload a smaller file.")
		return
	}

	contentType := strings.TrimSpace(req.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	uploadID := uuid.New().String()
	bucket := objStore.BucketUploads()
	key := fmt.Sprintf("uploads/%s/%s", uploadID, fileName)

	ctx := c.Request.Context()
	s3UploadID, err := objStore.CreateMultipart(ctx, bucket, key, contentType)
	if err != nil {
		response.InternalErrorf(c, "SERVER_ERROR", "Could not start the upload. Please try again.", err,
			"op", "s3.create_multipart", "uploadId", uploadID, "key", key)
		return
	}

	parts, err := presignParts(ctx, bucket, key, s3UploadID, allPartNumbers(totalParts))
	if err != nil {
		_ = objStore.AbortMultipart(ctx, bucket, key, s3UploadID)
		response.InternalErrorf(c, "SERVER_ERROR", "Could not start the upload. Please try again.", err,
			"op", "s3.presign_parts", "uploadId", uploadID, "key", key)
		return
	}

	uploadKey := uploadStateKey(uploadID)
	pipe := redisstore.Client.TxPipeline()
	pipe.HSet(ctx, uploadKey, map[string]interface{}{
		"fileName":     fileName,
		"declaredSize": req.FileSize,
		"contentType":  contentType,
		"bucket":       bucket,
		"key":          key,
		"s3UploadId":   s3UploadID,
		"partSize":     partSize,
		"totalParts":   totalParts,
		"createdAt":    time.Now().UTC().Format(time.RFC3339),
	})
	pipe.Expire(ctx, uploadKey, uploadTTL())
	if _, err := pipe.Exec(ctx); err != nil {
		_ = objStore.AbortMultipart(ctx, bucket, key, s3UploadID)
		response.InternalErrorf(c, "SERVER_ERROR", "Could not start the upload. Please try again.", err,
			"op", "redis.upload_init.pipeline", "uploadId", uploadID)
		return
	}

	response.Created(c, "Upload started successfully", gin.H{
		"uploadId":     uploadID,
		"key":          key,
		"partSize":     partSize,
		"totalParts":   totalParts,
		"urlExpiresAt": time.Now().UTC().Add(partURLExpiry).Format(time.RFC3339),
		"parts":        parts,
	})
}

// GetUploadParts re-presigns part upload URLs for an in-progress session —
// used to resume an interrupted upload or refresh expired URLs. With no
// partNumbers query parameter, all parts are re-presigned.
func GetUploadParts(c *gin.Context) {
	if objStore == nil {
		response.InternalError(c, "SERVER_ERROR", "File storage is unavailable. Please try again later.")
		return
	}
	uploadID := c.Param("uploadId")
	if uploadID == "" {
		response.BadRequest(c, "INVALID_INPUT", "Upload session not found. Please try again.")
		return
	}

	ctx := c.Request.Context()
	state, err := fetchUploadState(ctx, uploadID)
	if err != nil {
		if err == redis.Nil {
			response.NotFound(c, "NOT_FOUND", "Upload session expired. Please upload your file again.")
		} else {
			response.InternalErrorf(c, "SERVER_ERROR", "Could not load the upload session. Please try again.", err,
				"op", "redis.fetch_upload_state", "uploadId", uploadID)
		}
		return
	}

	totalParts, _ := strconv.Atoi(state["totalParts"])
	partNumbers, err := parsePartNumbers(c.Query("partNumbers"), totalParts)
	if err != nil {
		response.BadRequest(c, "INVALID_INPUT", err.Error())
		return
	}

	parts, err := presignParts(ctx, state["bucket"], state["key"], state["s3UploadId"], partNumbers)
	if err != nil {
		response.InternalErrorf(c, "SERVER_ERROR", "Could not refresh upload URLs. Please try again.", err,
			"op", "s3.presign_parts", "uploadId", uploadID)
		return
	}

	partSize, _ := strconv.ParseInt(state["partSize"], 10, 64)
	response.OK(c, "Upload URLs refreshed", gin.H{
		"uploadId":     uploadID,
		"partSize":     partSize,
		"urlExpiresAt": time.Now().UTC().Add(partURLExpiry).Format(time.RFC3339),
		"parts":        parts,
	})
}

// CompleteUpload finishes the S3 multipart upload from the client-collected
// part ETags, then verifies the true object size against the plan limit.
func CompleteUpload(c *gin.Context) {
	if objStore == nil {
		response.InternalError(c, "SERVER_ERROR", "File storage is unavailable. Please try again later.")
		return
	}
	uploadID := c.Param("uploadId")
	if uploadID == "" {
		response.BadRequest(c, "INVALID_INPUT", "Upload session not found. Please try again.")
		return
	}

	var req UploadCompleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Errorf(c, http.StatusBadRequest, "INVALID_INPUT", "Invalid completion request. Please try again.", err,
			"op", "bind_upload_complete", "uploadId", uploadID)
		return
	}

	ctx := c.Request.Context()
	state, err := fetchUploadState(ctx, uploadID)
	if err != nil {
		if err == redis.Nil {
			response.NotFound(c, "NOT_FOUND", "Upload session expired. Please upload your file again.")
		} else {
			response.InternalErrorf(c, "SERVER_ERROR", "Could not load the upload session. Please try again.", err,
				"op", "redis.fetch_upload_state", "uploadId", uploadID)
		}
		return
	}

	totalParts, _ := strconv.Atoi(state["totalParts"])
	if len(req.Parts) != totalParts {
		response.BadRequest(c, "UPLOAD_INCOMPLETE",
			fmt.Sprintf("Expected %d uploaded parts but received %d. Please finish uploading all parts first.", totalParts, len(req.Parts)))
		return
	}
	completed := make([]storage.CompletedPart, 0, len(req.Parts))
	for _, p := range req.Parts {
		if p.PartNumber < 1 || p.PartNumber > totalParts || p.ETag == "" {
			response.BadRequest(c, "INVALID_INPUT", "Invalid part list. Please restart the upload.")
			return
		}
		completed = append(completed, storage.CompletedPart{PartNumber: p.PartNumber, ETag: p.ETag})
	}

	bucket, key := state["bucket"], state["key"]
	if err := objStore.CompleteMultipart(ctx, bucket, key, state["s3UploadId"], completed); err != nil {
		response.Errorf(c, http.StatusBadRequest, "UPLOAD_INCOMPLETE",
			"The upload could not be finalized. Please re-upload the missing parts and try again.", err,
			"op", "s3.complete_multipart", "uploadId", uploadID, "key", key)
		return
	}

	info, err := objStore.StatObject(ctx, bucket, key)
	if err != nil {
		response.InternalErrorf(c, "SERVER_ERROR", "Could not verify the uploaded file. Please try again.", err,
			"op", "s3.stat_after_complete", "uploadId", uploadID, "key", key)
		return
	}

	// Re-verify against the plan limit using the TRUE size — the declared size
	// at init time is client-supplied and cannot be trusted.
	maxFileSizeMB := planMaxFileSizeMB(c)
	if info.Size > int64(maxFileSizeMB)*1024*1024 {
		_ = objStore.RemoveObject(ctx, bucket, key)
		_ = redisstore.Client.Del(ctx, uploadStateKey(uploadID)).Err()
		response.Err(c, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE",
			fmt.Sprintf("File size exceeds the %dMB limit for your plan", maxFileSizeMB))
		return
	}

	pipe := redisstore.Client.TxPipeline()
	pipe.HSet(ctx, uploadStateKey(uploadID), "size", info.Size)
	pipe.Expire(ctx, uploadStateKey(uploadID), uploadTTL())
	if _, err := pipe.Exec(ctx); err != nil {
		response.InternalErrorf(c, "SERVER_ERROR", "Could not finalize the upload. Please try again.", err,
			"op", "redis.upload_complete.record_size", "uploadId", uploadID)
		return
	}

	response.OK(c, "File uploaded successfully!", gin.H{
		"uploadId": uploadID,
		"fileName": state["fileName"],
		"size":     info.Size,
		"complete": true,
	})
}

// AbortUpload cancels an in-progress upload: the S3 multipart upload is
// aborted (freeing stored parts) and the Redis session is removed. Idempotent
// — aborting an unknown/expired session still returns 204.
func AbortUpload(c *gin.Context) {
	uploadID := c.Param("uploadId")
	if uploadID == "" {
		response.BadRequest(c, "INVALID_INPUT", "Upload session not found. Please try again.")
		return
	}

	ctx := c.Request.Context()
	state, err := fetchUploadState(ctx, uploadID)
	if err == nil && state["s3UploadId"] != "" && objStore != nil {
		if abortErr := objStore.AbortMultipart(ctx, state["bucket"], state["key"], state["s3UploadId"]); abortErr != nil {
			response.InternalErrorf(c, "SERVER_ERROR", "Could not cancel the upload. Please try again.", abortErr,
				"op", "s3.abort_multipart", "uploadId", uploadID, "key", state["key"])
			return
		}
	}
	if delErr := redisstore.Client.Del(ctx, uploadStateKey(uploadID), uploadChunkSetKey(uploadID)).Err(); delErr != nil {
		response.InternalErrorf(c, "SERVER_ERROR", "Could not cancel the upload. Please try again.", delErr,
			"op", "redis.upload_abort.del", "uploadId", uploadID)
		return
	}
	response.NoContent(c)
}

// GetUploadStatus returns the session metadata for an in-progress upload.
func GetUploadStatus(c *gin.Context) {
	uploadID := c.Param("uploadId")
	if uploadID == "" {
		response.BadRequest(c, "INVALID_INPUT", "Upload session not found. Please try again.")
		return
	}

	ctx := c.Request.Context()
	state, err := fetchUploadState(ctx, uploadID)
	if err != nil {
		if err == redis.Nil {
			response.NotFound(c, "NOT_FOUND", "Upload session expired. Please upload your file again.")
		} else {
			response.InternalErrorf(c, "SERVER_ERROR", "Upload session expired. Please upload your file again.", err,
				"op", "redis.fetch_upload_state", "uploadId", uploadID)
		}
		return
	}

	declaredSize, _ := strconv.ParseInt(state["declaredSize"], 10, 64)
	totalParts, _ := strconv.Atoi(state["totalParts"])
	response.OK(c, "Upload status loaded", gin.H{
		"uploadId":     uploadID,
		"fileName":     state["fileName"],
		"declaredSize": declaredSize,
		"totalParts":   totalParts,
	})
}

// UploadChunk is a one-release migration stub for the retired chunk-streaming
// protocol (PUT /api/uploads/:uploadId/chunk). Browsers running the old
// frontend bundle still call it after the backend switched to presigned
// multipart uploads; 410 Gone tells them to reload and pick up the new flow.
// Remove this stub (and its route) in the release after the frontend ships
// the presigned protocol.
func UploadChunk(c *gin.Context) {
	response.Err(c, http.StatusGone, "UPLOAD_PROTOCOL_CHANGED",
		"Please refresh the page to continue uploading.")
}

// presignParts returns browser-reachable presigned PUT URLs for the given
// part numbers of an in-progress multipart upload.
func presignParts(ctx context.Context, bucket, key, s3UploadID string, partNumbers []int) ([]uploadPartURL, error) {
	parts := make([]uploadPartURL, 0, len(partNumbers))
	for _, n := range partNumbers {
		u, err := objStore.PresignUploadPart(ctx, bucket, key, s3UploadID, n, partURLExpiry)
		if err != nil {
			return nil, fmt.Errorf("presign part %d: %w", n, err)
		}
		parts = append(parts, uploadPartURL{PartNumber: n, URL: u})
	}
	return parts, nil
}

// allPartNumbers returns [1..totalParts].
func allPartNumbers(totalParts int) []int {
	nums := make([]int, totalParts)
	for i := range nums {
		nums[i] = i + 1
	}
	return nums
}

// parsePartNumbers parses a comma-separated part-number list ("2,3"); an
// empty value means every part. Each number must be within [1, totalParts].
func parsePartNumbers(raw string, totalParts int) ([]int, error) {
	if strings.TrimSpace(raw) == "" {
		return allPartNumbers(totalParts), nil
	}
	fields := strings.Split(raw, ",")
	nums := make([]int, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil || n < 1 || n > totalParts {
			return nil, fmt.Errorf("invalid part number %q", strings.TrimSpace(f))
		}
		nums = append(nums, n)
	}
	return nums, nil
}

// sanitizeFileName reduces a client-supplied file name to a safe base name.
// Returns "" when nothing usable remains (empty, dot-only, path-only input).
func sanitizeFileName(name string) string {
	base := filepath.Base(strings.TrimSpace(name))
	if base == "." || base == ".." || base == string(filepath.Separator) {
		return ""
	}
	return base
}

func fetchUploadState(ctx context.Context, uploadID string) (map[string]string, error) {
	state, err := redisstore.Client.HGetAll(ctx, uploadStateKey(uploadID)).Result()
	if err != nil {
		return nil, err
	}
	if len(state) == 0 {
		return nil, redis.Nil
	}
	return state, nil
}

func uploadStateKey(uploadID string) string {
	return fmt.Sprintf("upload:%s", uploadID)
}

// uploadChunkSetKey is the legacy chunk-set key from the retired chunked
// protocol. Still deleted alongside the state key so stale sessions left by
// the old protocol get cleaned up.
func uploadChunkSetKey(uploadID string) string {
	return fmt.Sprintf("upload:%s:chunks", uploadID)
}

// uploadPartSize resolves the multipart part size from UPLOAD_PART_SIZE_MB
// (default 8 MiB), clamped to the S3 5 MiB minimum for non-terminal parts.
func uploadPartSize() int64 {
	mb := defaultPartSizeMB
	if value := os.Getenv("UPLOAD_PART_SIZE_MB"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			mb = parsed
		}
	}
	size := int64(mb) * 1024 * 1024
	if size < minPartSizeBytes {
		size = minPartSizeBytes
	}
	return size
}

func uploadTTL() time.Duration {
	return config.UploadTTL()
}

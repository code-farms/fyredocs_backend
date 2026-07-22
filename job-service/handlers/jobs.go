package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"fyredocs/shared/config"
	"fyredocs/shared/logger"
	"fyredocs/shared/natsconn"
	"fyredocs/shared/queue"
	"fyredocs/shared/redisstore"
	"fyredocs/shared/response"
	"fyredocs/shared/storage"

	"job-service/internal/models"
	"job-service/internal/routing"
)

// outputFileCache caches FileMetadata lookups for completed job downloads.
// Entries are immutable once a job is completed, so no TTL is needed.
var outputFileCache sync.Map // uuid.UUID -> models.FileMetadata

// allowedMIMETypes maps tool types to their expected MIME content types.
var allowedMIMETypes = map[string][]string{
	"pdf":   {"application/pdf"},
	"word":  {"application/msword", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "application/zip"},
	"excel": {"application/vnd.ms-excel", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "application/zip"},
	"ppt":   {"application/vnd.ms-powerpoint", "application/vnd.openxmlformats-officedocument.presentationml.presentation", "application/zip"},
	"image": {"image/png", "image/jpeg", "image/webp"},
	"odt":   {"application/vnd.oasis.opendocument.text", "application/zip"},
	"ods":   {"application/vnd.oasis.opendocument.spreadsheet", "application/zip"},
	"odp":   {"application/vnd.oasis.opendocument.presentation", "application/zip"},
}

// UploadJobRequest is the job-creation payload. It accepts either a single
// UploadID or a batch via UploadIDs, plus tool-specific Options.
type UploadJobRequest struct {
	UploadID  string          `json:"uploadId"`
	UploadIDs []string        `json:"uploadIds"`
	Options   json.RawMessage `json:"options"`
}

// CreateJobFromTool validates the tool and upload, enforces per-key idempotency,
// persists a ProcessingJob, and publishes it to the owning worker service's
// queue for asynchronous processing.
func CreateJobFromTool(c *gin.Context) {
	toolType, err := normalizeToolType(strings.TrimSpace(c.Param("tool")))
	if err != nil {
		response.BadRequest(c, "INVALID_INPUT", err.Error())
		return
	}

	if routing.ServiceForTool(toolType) == "" {
		response.BadRequest(c, "INVALID_INPUT", "This tool is not available.")
		return
	}

	if objStore == nil {
		response.InternalError(c, "SERVER_ERROR", "File storage is unavailable. Please try again later.")
		return
	}

	jobID := uuid.New()

	// Idempotency: an Idempotency-Key header dedups retries of the same logical
	// request. Reserve it ATOMICALLY (SetNX) with this request's jobID so two
	// concurrent requests carrying the same key cannot both create a job (finding
	// B2 — the old GET-then-SET was a check-then-set race). The reservation is
	// deleted if we fail before the job is committed, so a client can safely retry.
	idempotencyKey := c.GetHeader("Idempotency-Key")
	idempotencyRedisKey := ""
	idempotencyReserved := false
	idempotencyCommitted := false
	if idempotencyKey != "" && redisstore.Client != nil {
		idempotencyRedisKey = fmt.Sprintf("idempotency:%s", idempotencyKey)
		reserved, err := redisstore.Client.SetNX(c.Request.Context(), idempotencyRedisKey, jobID.String(), idempotencyKeyTTL).Result()
		if err == nil && !reserved {
			// The key is already owned by a prior/concurrent request. Return its
			// committed job, or ask the client to wait rather than duplicating.
			if existing, gerr := redisstore.Client.Get(c.Request.Context(), idempotencyRedisKey).Result(); gerr == nil && existing != "" {
				var existingJob models.ProcessingJob
				if dberr := models.DB.First(&existingJob, "id = ?", existing).Error; dberr == nil {
					guestTok := assignGuestTokenIfNeeded(c, authUserID(c), existingJob.ID)
					response.Created(c, "Your file is being processed!", createJobResponse{
						jobResponse: toJobResponse(existingJob),
						GuestToken:  guestTok,
					})
					return
				}
			}
			response.Err(c, http.StatusConflict, "ALREADY_PROCESSING", "This request is already being processed. Please wait a moment.")
			return
		}
		idempotencyReserved = err == nil && reserved
	}
	// Release the idempotency reservation if we bail before committing the job, so
	// a same-key retry isn't blocked pointing at a job that never existed.
	defer func() {
		if idempotencyReserved && !idempotencyCommitted && redisstore.Client != nil {
			_ = redisstore.Client.Del(context.Background(), idempotencyRedisKey).Err()
		}
	}()

	var totalSize int64
	var inputPaths []string
	var fileMetas []models.FileMetadata
	var consumedUploadIDs []string
	originalName := ""
	optionsRaw := ""

	// Objects streamed directly to storage in the multipart branch live at
	// uploads/{jobID}/... and belong only to this request. If the job is never
	// queued (a DB/publish failure below), delete them so a failed create doesn't
	// leak storage. (Presigned uploads are intentionally preserved for retry and
	// reaped by the stale-multipart sweep — they are NOT tracked here.)
	var directUploadKeys []string
	jobQueued := false
	defer func() {
		if jobQueued || len(directUploadKeys) == 0 || objStore == nil {
			return
		}
		ctx := context.Background()
		for _, key := range directUploadKeys {
			if err := objStore.RemoveObject(ctx, objStore.BucketUploads(), key); err != nil {
				logger.LogWarn(ctx, "s3.cleanup_direct_upload", err, "key", key, "jobId", jobID)
			}
		}
	}()

	if strings.Contains(c.GetHeader("Content-Type"), "application/json") {
		var uploadReq UploadJobRequest
		if err := c.ShouldBindJSON(&uploadReq); err != nil {
			response.Errorf(c, http.StatusBadRequest, "INVALID_INPUT", "Invalid request. Please try again.", err,
				"op", "bind_job_request", "tool", toolType)
			return
		}
		uploadIDs := uploadReq.UploadIDs
		if len(uploadIDs) == 0 && uploadReq.UploadID != "" {
			uploadIDs = []string{uploadReq.UploadID}
		}
		if len(uploadIDs) == 0 {
			response.BadRequest(c, "INVALID_INPUT", "Please upload a file first.")
			return
		}

		maxFiles := planMaxFilesPerJob(c)
		if len(uploadIDs) > maxFiles {
			publishPlanLimitHit(c, "max_files", toolType)
			response.BadRequest(c, "TOO_MANY_FILES",
				fmt.Sprintf("Your plan allows up to %d files per job", maxFiles))
			return
		}

		// Same-uploadId replay: a duplicate POST after the first one already
		// consumed the upload would otherwise hit "upload not found" because
		// releaseUpload cleared the Redis state. Return the original job so
		// the client transparently resumes polling on the same jobId.
		if existingJob, ok := findExistingJobForUploads(c.Request.Context(), uploadIDs); ok {
			guestTok := assignGuestTokenIfNeeded(c, authUserID(c), existingJob.ID)
			response.Created(c, "Your file is being processed!", createJobResponse{
				jobResponse: toJobResponse(*existingJob),
				GuestToken:  guestTok,
			})
			return
		}

		optionsRaw = string(uploadReq.Options)
		// Validate before consuming uploads so a rejected request doesn't
		// burn the same-uploadId idempotency record.
		if err := validateToolOptions(toolType, optionsRaw); err != nil {
			response.BadRequest(c, "INVALID_OPTIONS", err.Error())
			return
		}

		// Atomically claim the uploads so a concurrent duplicate submission
		// (double-click, client retry, two tabs) cannot consume the same upload
		// and create a second job (finding B1). Claims are released on return;
		// the winner's consumed-upload record dedups later duplicates.
		claimed, conflict, cerr := claimUploads(c.Request.Context(), uploadIDs)
		if cerr != nil {
			response.InternalErrorf(c, "SERVER_ERROR", "Something went wrong. Please try again.", cerr,
				"op", "claim_uploads", "tool", toolType, "jobId", jobID)
			return
		}
		if conflict {
			// A concurrent request already holds these uploads. If it has
			// committed, return its job; otherwise ask the client to wait.
			if existingJob, ok := findExistingJobForUploads(c.Request.Context(), uploadIDs); ok {
				guestTok := assignGuestTokenIfNeeded(c, authUserID(c), existingJob.ID)
				response.Created(c, "Your file is being processed!", createJobResponse{
					jobResponse: toJobResponse(*existingJob),
					GuestToken:  guestTok,
				})
				return
			}
			response.Err(c, http.StatusConflict, "ALREADY_PROCESSING", "This file is already being processed. Please wait a moment.")
			return
		}
		defer releaseUploadClaims(context.Background(), claimed)

		for _, uploadID := range uploadIDs {
			consumed, err := consumeUpload(c.Request.Context(), toolType, uploadID)
			if err != nil {
				response.Errorf(c, http.StatusBadRequest, "INVALID_INPUT", err.Error(), err,
					"op", "consume_upload", "uploadId", uploadID, "tool", toolType, "jobId", jobID)
				return
			}
			consumedUploadIDs = append(consumedUploadIDs, uploadID)
			totalSize += consumed.Size
			inputPaths = append(inputPaths, consumed.Key)
			fileMetas = append(fileMetas, models.FileMetadata{
				ID:           uuid.New(),
				JobID:        jobID,
				Kind:         "input",
				OriginalName: consumed.OriginalName,
				Path:         consumed.Key,
				SizeBytes:    consumed.Size,
			})
			if originalName == "" {
				originalName = consumed.OriginalName
			}
		}
	} else {
		form, err := c.MultipartForm()
		if err != nil {
			response.Errorf(c, http.StatusBadRequest, "INVALID_INPUT", "Invalid file upload. Please try again.", err,
				"op", "parse_multipart", "tool", toolType)
			return
		}
		files := form.File["files"]
		if len(files) == 0 {
			response.BadRequest(c, "INVALID_INPUT", "Please upload a file first.")
			return
		}

		maxFiles := planMaxFilesPerJob(c)
		if len(files) > maxFiles {
			publishPlanLimitHit(c, "max_files", toolType)
			response.BadRequest(c, "TOO_MANY_FILES",
				fmt.Sprintf("Your plan allows up to %d files per job", maxFiles))
			return
		}

		if len(form.Value["options"]) > 0 {
			optionsRaw = form.Value["options"][0]
		}
		if err := validateToolOptions(toolType, optionsRaw); err != nil {
			response.BadRequest(c, "INVALID_OPTIONS", err.Error())
			return
		}
		originalName = files[0].Filename
		if toolType == "merge-pdf" {
			originalName = "merged.pdf"
		}

		maxFileSizeMB := planMaxFileSizeMB(c)
		maxSize := int64(maxFileSizeMB) * 1024 * 1024
		for _, file := range files {
			if file.Size > maxSize {
				publishPlanLimitHit(c, "max_file_size", toolType)
				response.Err(c, 413, "FILE_TOO_LARGE",
					fmt.Sprintf("File size exceeds the %dMB limit for your plan", maxFileSizeMB))
				return
			}
			if err := validateFileType(toolType, file.Filename); err != nil {
				response.Errorf(c, http.StatusBadRequest, "INVALID_INPUT", err.Error(), err,
					"op", "validate_file_type", "tool", toolType, "fileName", file.Filename)
				return
			}
			key, err := storeDirectUpload(c.Request.Context(), toolType, jobID.String(), file)
			if err != nil {
				if errors.As(err, new(invalidInputError)) {
					response.Errorf(c, http.StatusBadRequest, "INVALID_INPUT", err.Error(), err,
						"op", "validate_mime", "tool", toolType, "fileName", file.Filename, "jobId", jobID)
					return
				}
				response.InternalErrorf(c, "SERVER_ERROR", "Something went wrong. Please try again.", err,
					"op", "store_direct_upload", "tool", toolType, "fileName", file.Filename, "jobId", jobID)
				return
			}
			totalSize += file.Size
			inputPaths = append(inputPaths, key)
			directUploadKeys = append(directUploadKeys, key)
			fileMetas = append(fileMetas, models.FileMetadata{
				ID:           uuid.New(),
				JobID:        jobID,
				Kind:         "input",
				OriginalName: file.Filename,
				Path:         key,
				SizeBytes:    file.Size,
			})
		}
	}

	if toolType == "merge-pdf" {
		originalName = "merged.pdf"
	} else if originalName == "" {
		originalName = "document.pdf"
	}

	userID := authUserID(c)
	planName := c.GetHeader("X-User-Plan")
	expiresAt := jobExpiry(userID, planName)
	correlationID := uuid.NewString()

	metaPayload := map[string]interface{}{
		"options":       parseOptions(optionsRaw),
		"correlationId": correlationID,
	}
	metaBytes, err := json.Marshal(metaPayload)
	if err != nil {
		response.InternalErrorf(c, "SERVER_ERROR", "Something went wrong. Please try again.", err,
			"op", "marshal_job_meta", "tool", toolType, "jobId", jobID)
		return
	}

	job := models.ProcessingJob{
		ID:        jobID,
		UserID:    userID,
		ToolType:  toolType,
		Status:    "queued",
		Progress:  0,
		FileName:  originalName,
		FileSize:  totalSize,
		Metadata:  datatypes.JSON(metaBytes),
		ExpiresAt: expiresAt,
	}

	txCtx, txCancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer txCancel()
	if err := models.DB.WithContext(txCtx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&job).Error; err != nil {
			return logger.LogErr(c.Request.Context(), "db.processing_jobs.create", err,
				"jobId", job.ID, "tool", toolType)
		}
		for _, meta := range fileMetas {
			if err := tx.Create(&meta).Error; err != nil {
				return logger.LogErr(c.Request.Context(), "db.file_metadata.create", err,
					"jobId", job.ID, "tool", toolType, "fileMetaId", meta.ID)
			}
		}
		return nil
	}); err != nil {
		response.InternalErrorf(c, "SERVER_ERROR", "Something went wrong. Please try again.", err,
			"op", "db.processing_jobs.transaction", "tool", toolType, "jobId", jobID)
		return
	}
	// The job row now exists; the Idempotency-Key reservation (set to this jobID
	// up front) is authoritative, so the deferred cleanup must not delete it.
	idempotencyCommitted = true

	guestTok := assignGuestTokenIfNeeded(c, userID, jobID)

	// Use centralized tool-to-service mapping
	serviceName := routing.ServiceForTool(toolType)
	if serviceName == "" {
		response.BadRequest(c, "INVALID_INPUT", "This tool is not available.")
		return
	}
	event := queue.JobEvent{
		EventType:     "JobCreated",
		JobID:         job.ID.String(),
		ToolType:      toolType,
		InputPaths:    inputPaths,
		Options:       optionsPayload(optionsRaw),
		CorrelationID: correlationID,
		Timestamp:     time.Now().UTC(),
	}
	subject := queue.SubjectForDispatch(serviceName)
	if err := queue.PublishJobEvent(c.Request.Context(), natsconn.JS, subject, event); err != nil {
		response.InternalErrorf(c, "SERVER_ERROR", "Our servers are busy. Please try again in a moment.", err,
			"op", "nats.publish_job_event", "subject", subject, "tool", toolType, "jobId", job.ID)
		return
	}
	// Job committed and dispatched — keep the direct-upload objects (the worker
	// will consume them); the deferred cleanup is now a no-op.
	jobQueued = true

	// Free the upload slot only after the job is fully committed and queued.
	// On any failure above this line, the upload state is preserved so the
	// frontend can retry with the same uploadId without re-uploading (the
	// object stays at its uploads/{uploadId}/... key). Record the upload->job
	// mapping before releasing so a duplicate POST with the same uploadId
	// returns the original job instead of "upload not found".
	for _, id := range consumedUploadIDs {
		recordConsumedUpload(c.Request.Context(), id, job.ID.String())
		releaseUpload(c.Request.Context(), id)
	}

	// The Idempotency-Key was reserved with this jobID up front (see the top of
	// the handler), so no post-commit write is needed here.

	publishJobAnalyticsEvent(c.Request.Context(), "job.created", job.ID.String(), toolType, userID, totalSize)
	slog.Info("job queued", "jobId", job.ID, "tool", toolType, "correlationId", correlationID)
	response.Created(c, "Your file is being processed!", createJobResponse{
		jobResponse: toJobResponse(job),
		GuestToken:  guestTok,
	})
}

// GetJobsByTool returns the caller's jobs for the given tool type, paginated and
// ordered most-recent-first.
func GetJobsByTool(c *gin.Context) {
	toolType, err := normalizeToolType(strings.TrimSpace(c.Param("tool")))
	if err != nil {
		response.BadRequest(c, "INVALID_INPUT", err.Error())
		return
	}

	limit := clampInt(queryInt(c, "limit", 25), 1, 100)
	page := clampInt(queryInt(c, "page", 1), 1, 100000)
	offset := (page - 1) * limit

	userID := authUserID(c)
	if userID == nil {
		jobIDs := guestJobIDs(c.Request.Context(), guestToken(c))
		if len(jobIDs) == 0 {
			response.OKWithMeta(c, "Jobs loaded successfully", []jobResponse{}, &response.Meta{Page: page, Limit: limit, Total: 0})
			return
		}
		var jobs []models.ProcessingJob
		if err := models.DB.Where("id IN ? AND tool_type = ? AND user_id IS NULL", jobIDs, toolType).
			Order("created_at desc").
			Limit(limit).Offset(offset).
			Find(&jobs).Error; err != nil {
			response.InternalErrorf(c, "SERVER_ERROR", "Could not load your jobs. Please try again.", err,
				"op", "db.processing_jobs.list_guest", "tool", toolType, "page", page, "limit", limit)
			return
		}
		response.OKWithMeta(c, "Jobs loaded successfully", toJobResponses(jobs), &response.Meta{Page: page, Limit: limit})
		return
	}

	var jobs []models.ProcessingJob
	if err := models.DB.Where("user_id = ? AND tool_type = ?", userID, toolType).
		Order("created_at desc").
		Limit(limit).Offset(offset).
		Find(&jobs).Error; err != nil {
		response.InternalErrorf(c, "SERVER_ERROR", "Could not load your jobs. Please try again.", err,
			"op", "db.processing_jobs.list_user", "tool", toolType, "userId", userID, "page", page, "limit", limit)
		return
	}
	response.OKWithMeta(c, "Jobs loaded successfully", toJobResponses(jobs), &response.Meta{Page: page, Limit: limit})
}

// GetJobByID returns a single job the caller is authorized to see. Unknown and
// unauthorized jobs are both reported as not found to avoid leaking existence.
func GetJobByID(c *gin.Context) {
	toolType, err := normalizeToolType(strings.TrimSpace(c.Param("tool")))
	if err != nil {
		response.BadRequest(c, "INVALID_INPUT", err.Error())
		return
	}
	jobID := c.Param("id")

	var job models.ProcessingJob
	if err := models.DB.First(&job, "id = ? AND tool_type = ?", jobID, toolType).Error; err != nil {
		response.NotFound(c, "NOT_FOUND", "Job not found or has expired.")
		return
	}

	if !authorizeJobAccess(c, &job) {
		response.NotFound(c, "NOT_FOUND", "Job not found or has expired.")
		return
	}

	response.OK(c, "Job details loaded", toJobResponse(job))
}

// DeleteJobByID removes a job the caller owns along with its stored input and
// output objects. Object removal is best-effort; the DB rows are always deleted.
func DeleteJobByID(c *gin.Context) {
	toolType, err := normalizeToolType(strings.TrimSpace(c.Param("tool")))
	if err != nil {
		response.BadRequest(c, "INVALID_INPUT", err.Error())
		return
	}
	jobID := c.Param("id")

	var job models.ProcessingJob
	if err := models.DB.First(&job, "id = ? AND tool_type = ?", jobID, toolType).Error; err != nil {
		response.NotFound(c, "NOT_FOUND", "Job not found or has expired.")
		return
	}
	if !authorizeJobAccess(c, &job) {
		response.NotFound(c, "NOT_FOUND", "Job not found or has expired.")
		return
	}

	var files []models.FileMetadata
	if err := models.DB.Where("job_id = ?", job.ID).Find(&files).Error; err != nil {
		slog.Error("failed to fetch file metadata for deletion", "jobId", job.ID, "error", err)
	}
	for _, file := range files {
		// Legacy rows from the pre-S3 protocol stored absolute disk paths —
		// there is nothing to remove from object storage for those.
		if strings.HasPrefix(file.Path, "/") {
			continue
		}
		if objStore == nil {
			slog.Warn("object store unavailable, skipping object removal", "path", file.Path)
			continue
		}
		if err := objStore.RemoveObject(c.Request.Context(), bucketFor(file.Kind), file.Path); err != nil {
			slog.Warn("failed to remove object", "path", file.Path, "kind", file.Kind, "error", err)
		}
	}
	if err := models.DB.Where("job_id = ?", job.ID).Delete(&models.FileMetadata{}).Error; err != nil {
		slog.Error("failed to delete file metadata", "jobId", job.ID, "error", err)
	}

	if err := models.DB.Delete(&job).Error; err != nil {
		response.InternalErrorf(c, "SERVER_ERROR", "Could not delete the job. Please try again.", err,
			"op", "db.processing_jobs.delete", "jobId", job.ID, "tool", toolType)
		return
	}

	removeGuestJob(c.Request.Context(), guestToken(c), job.ID)
	response.NoContent(c)
}

// DownloadJobFile redirects an authorized caller to a short-lived presigned URL
// for a completed job's output. It rejects jobs that are not yet completed.
func DownloadJobFile(c *gin.Context) {
	toolType, err := normalizeToolType(strings.TrimSpace(c.Param("tool")))
	if err != nil {
		response.BadRequest(c, "INVALID_INPUT", err.Error())
		return
	}
	jobID := c.Param("id")

	var job models.ProcessingJob
	if err := models.DB.First(&job, "id = ? AND tool_type = ?", jobID, toolType).Error; err != nil {
		response.NotFound(c, "NOT_FOUND", "Job not found or has expired.")
		return
	}
	if !authorizeJobAccess(c, &job) {
		response.NotFound(c, "NOT_FOUND", "Job not found or has expired.")
		return
	}
	if job.Status != "completed" {
		response.BadRequest(c, "NOT_READY", "Your file is still being processed. Please wait.")
		return
	}

	var outputFile models.FileMetadata
	if cached, ok := outputFileCache.Load(job.ID); ok {
		outputFile = cached.(models.FileMetadata)
	} else if err := models.DB.First(&outputFile, "job_id = ? AND kind = ?", job.ID, "output").Error; err != nil {
		response.NotFound(c, "NOT_FOUND", "This download link has expired. Please process your file again.")
		return
	} else {
		outputFileCache.Store(job.ID, outputFile)
	}

	fileName, contentType := outputFileName(job.ToolType, job.FileName, job.Metadata)
	redirectToOutput(c, outputFile.Path, fileName, contentType)
}

// downloadURLExpiry is how long a presigned output download URL stays valid.
const downloadURLExpiry = 5 * time.Minute

// redirectToOutput 302-redirects the client to a short-lived presigned GET
// URL for the output object, forcing attachment disposition (RFC-safe via
// mime.FormatMediaType) and the computed content type. Legacy rows whose Path
// is an absolute disk path (pre-S3 protocol) can no longer be served and
// yield 404.
func redirectToOutput(c *gin.Context, key string, fileName string, contentType string) {
	if strings.HasPrefix(key, "/") {
		response.NotFound(c, "NOT_FOUND", "This download link has expired. Please process your file again.")
		return
	}
	if objStore == nil {
		response.InternalError(c, "SERVER_ERROR", "File storage is unavailable. Please try again later.")
		return
	}
	params := url.Values{}
	params.Set("response-content-disposition", mime.FormatMediaType("attachment", map[string]string{"filename": fileName}))
	params.Set("response-content-type", contentType)
	signed, err := objStore.PresignGet(c.Request.Context(), objStore.BucketOutputs(), key, downloadURLExpiry, params)
	if err != nil {
		response.InternalErrorf(c, "SERVER_ERROR", "Could not prepare the download. Please try again.", err,
			"op", "s3.presign_get", "key", key)
		return
	}
	c.Redirect(http.StatusFound, signed)
}

// GetJobHistory returns the authenticated user's jobs, most recent first, with
// pagination. Guests have no history and are rejected as unauthorized.
func GetJobHistory(c *gin.Context) {
	userID := authUserID(c)
	if userID == nil {
		response.Unauthorized(c, "UNAUTHORIZED", "Please log in to view your job history.")
		return
	}

	limit := clampInt(queryInt(c, "limit", 25), 1, 100)
	page := clampInt(queryInt(c, "page", 1), 1, 100000)
	offset := (page - 1) * limit

	var jobs []models.ProcessingJob
	if err := models.DB.Where("user_id = ?", userID).
		Order("created_at desc").
		Limit(limit).
		Offset(offset).
		Find(&jobs).Error; err != nil {
		response.InternalErrorf(c, "SERVER_ERROR", "Could not load your job history. Please try again.", err,
			"op", "db.processing_jobs.list_history", "userId", userID, "page", page, "limit", limit)
		return
	}

	response.OKWithMeta(c, "Job history loaded", toJobResponses(jobs), &response.Meta{Page: page, Limit: limit})
}

// consumedUpload describes an uploaded object that a new job will consume.
type consumedUpload struct {
	// Key is the object key inside the uploads bucket
	// (uploads/{uploadId}/{fileName}).
	Key          string
	OriginalName string
	Size         int64
}

// consumeUpload validates a completed presigned-multipart upload session for
// job creation: the Redis state must exist, the object must exist in the
// uploads bucket (its true size comes from StatObject, never from the
// client), and its first 512 bytes must sniff to a MIME type allowed for the
// tool. Nothing is moved or deleted here — the object stays at its
// uploads/{uploadId}/... key (workers read it from there) and the Redis state
// is preserved so the caller can retry on a downstream failure (DB
// transaction, queue publish) without forcing the user to re-upload. The
// caller must invoke releaseUpload after the job is committed to free the
// upload slot.
func consumeUpload(ctx context.Context, toolType string, uploadID string) (consumedUpload, error) {
	if uploadID == "" {
		return consumedUpload{}, fmt.Errorf("uploadId is required")
	}
	if redisstore.Client == nil {
		return consumedUpload{}, logger.LogErr(ctx, "consume_upload.redis_unavailable",
			fmt.Errorf("redis client is nil"), "uploadId", uploadID)
	}
	if objStore == nil {
		return consumedUpload{}, logger.LogErr(ctx, "consume_upload.storage_unavailable",
			fmt.Errorf("object store is nil"), "uploadId", uploadID)
	}
	state, err := redisstore.Client.HGetAll(ctx, uploadStateKey(uploadID)).Result()
	if err != nil {
		if err == redis.Nil {
			logger.LogWarn(ctx, "consume_upload.not_found", err, "uploadId", uploadID)
			return consumedUpload{}, fmt.Errorf("upload not found")
		}
		logger.LogErr(ctx, "consume_upload.redis_hgetall", err, "uploadId", uploadID)
		return consumedUpload{}, fmt.Errorf("failed to read upload state")
	}
	if len(state) == 0 {
		return consumedUpload{}, fmt.Errorf("upload not found")
	}
	fileName := state["fileName"]
	if fileName == "" {
		return consumedUpload{}, logger.LogErr(ctx, "consume_upload.missing_filename",
			fmt.Errorf("redis upload state has no fileName"), "uploadId", uploadID)
	}
	if err := validateFileType(toolType, fileName); err != nil {
		return consumedUpload{}, err
	}
	key := state["key"]
	if key == "" {
		return consumedUpload{}, logger.LogErr(ctx, "consume_upload.missing_key",
			fmt.Errorf("redis upload state has no object key"), "uploadId", uploadID)
	}
	bucket := state["bucket"]
	if bucket == "" {
		bucket = objStore.BucketUploads()
	}

	info, err := objStore.StatObject(ctx, bucket, key)
	if err != nil {
		if storage.IsNotFound(err) {
			logger.LogWarn(ctx, "consume_upload.object_missing", err, "uploadId", uploadID, "key", key)
			return consumedUpload{}, fmt.Errorf("uploaded file not found — please finish uploading first")
		}
		logger.LogErr(ctx, "consume_upload.stat", err, "uploadId", uploadID, "key", key)
		return consumedUpload{}, fmt.Errorf("failed to verify the uploaded file")
	}
	if info.Size > maxUploadBytes() {
		return consumedUpload{}, fmt.Errorf("file exceeds maximum size")
	}

	head, err := objStore.GetObjectRange(ctx, bucket, key, 0, sniffLen)
	if err != nil {
		logger.LogErr(ctx, "consume_upload.sniff_read", err, "uploadId", uploadID, "key", key)
		return consumedUpload{}, fmt.Errorf("failed to verify the uploaded file")
	}
	if err := validateMIMEHead(toolType, head); err != nil {
		return consumedUpload{}, err
	}

	return consumedUpload{Key: key, OriginalName: fileName, Size: info.Size}, nil
}

// releaseUpload clears the Redis session state for a successfully consumed
// upload. The object itself is NOT removed — workers read it from its
// uploads/{uploadId}/... key; the in-process cleanup loop reaps it later. Failures are
// logged but never returned — the job has already been queued and a stuck
// upload record will be cleaned up by the Redis TTL.
func releaseUpload(ctx context.Context, uploadID string) {
	if uploadID == "" || redisstore.Client == nil {
		return
	}
	if err := redisstore.Client.Del(ctx, uploadStateKey(uploadID), uploadChunkSetKey(uploadID)).Err(); err != nil {
		slog.Warn("failed to clear upload state", "uploadId", uploadID, "error", err)
	}
}

// consumedUploadIdempotencyTTL bounds how long a same-uploadId replay is
// deduplicated to the original job. Matches the Idempotency-Key TTL.
const consumedUploadIdempotencyTTL = 10 * time.Minute

// idempotencyKeyTTL bounds how long an Idempotency-Key reservation lives.
const idempotencyKeyTTL = 10 * time.Minute

// uploadClaimTTL bounds a per-upload processing claim (finding B1). It is only a
// safety net: the claim is released when the request returns, and expires on its
// own if the process dies mid-flight.
const uploadClaimTTL = 10 * time.Minute

func consumedUploadKey(uploadID string) string {
	return "idempotency:upload:" + uploadID
}

func uploadClaimKey(uploadID string) string {
	return "claim:upload:" + uploadID
}

// claimUploads atomically reserves every uploadID so that two concurrent job
// submissions cannot consume the same upload and create duplicate jobs
// (finding B1 — quota/compute double-spend). It returns claimed=all-ids only when
// EVERY id was newly reserved; on any id already held it releases what it took and
// returns conflict=true; a Redis error returns err. The caller releases the
// claims on return (see releaseUploadClaims); the winner's consumed-upload record
// dedups any later duplicate before it ever reaches this claim.
func claimUploads(ctx context.Context, uploadIDs []string) (claimed []string, conflict bool, err error) {
	if redisstore.Client == nil {
		return nil, false, nil
	}
	for _, id := range uploadIDs {
		ok, e := redisstore.Client.SetNX(ctx, uploadClaimKey(id), "1", uploadClaimTTL).Result()
		if e != nil {
			releaseUploadClaims(ctx, claimed)
			return nil, false, e
		}
		if !ok {
			releaseUploadClaims(ctx, claimed)
			return nil, true, nil
		}
		claimed = append(claimed, id)
	}
	return claimed, false, nil
}

// releaseUploadClaims deletes the processing claims taken by claimUploads. Uses a
// background context so the release still runs if the request context is already
// cancelled; a missed delete is harmless (the claim expires on uploadClaimTTL).
func releaseUploadClaims(ctx context.Context, uploadIDs []string) {
	if redisstore.Client == nil || len(uploadIDs) == 0 {
		return
	}
	keys := make([]string, 0, len(uploadIDs))
	for _, id := range uploadIDs {
		keys = append(keys, uploadClaimKey(id))
	}
	_ = redisstore.Client.Del(ctx, keys...).Err()
}

// recordConsumedUpload remembers which job consumed an uploadId so a duplicate
// submission with the same uploadId returns the original job rather than
// "upload not found". Failures are logged but not returned — the job is
// already queued; losing the dedup record only re-exposes the existing
// "upload not found" symptom on a duplicate POST, never anything worse.
func recordConsumedUpload(ctx context.Context, uploadID string, jobID string) {
	if uploadID == "" || jobID == "" || redisstore.Client == nil {
		return
	}
	if err := redisstore.Client.Set(ctx, consumedUploadKey(uploadID), jobID, consumedUploadIdempotencyTTL).Err(); err != nil {
		slog.Warn("failed to record consumed upload", "uploadId", uploadID, "jobId", jobID, "error", err)
	}
}

// findExistingJobForUploads returns the job that previously consumed every one
// of the given uploadIds, if and only if all of them resolve to the same jobId
// and that job still exists in the DB. Any miss, mismatch, or DB lookup
// failure returns (nil, false) so the caller falls through to the normal
// create-job flow.
func findExistingJobForUploads(ctx context.Context, uploadIDs []string) (*models.ProcessingJob, bool) {
	if len(uploadIDs) == 0 || redisstore.Client == nil {
		return nil, false
	}
	var jobID string
	for _, id := range uploadIDs {
		if id == "" {
			return nil, false
		}
		got, err := redisstore.Client.Get(ctx, consumedUploadKey(id)).Result()
		if err != nil || got == "" {
			return nil, false
		}
		if jobID == "" {
			jobID = got
		} else if got != jobID {
			return nil, false
		}
	}
	var existing models.ProcessingJob
	if err := models.DB.First(&existing, "id = ?", jobID).Error; err != nil {
		return nil, false
	}
	return &existing, true
}

// sniffLen is how many leading bytes feed http.DetectContentType (the
// maximum the standard library considers).
const sniffLen = 512

// invalidInputError marks failures caused by the client's file content (e.g.
// a MIME sniff mismatch) so handlers can map them to 400 instead of 500.
type invalidInputError struct{ msg string }

func (e invalidInputError) Error() string { return e.msg }

// storeDirectUpload streams one multipart file straight into the uploads
// bucket under uploads/{jobID}/{fileName}, teeing off the first 512 bytes for
// MIME validation without buffering the whole file in memory. Used by the
// direct-multipart job-creation branch (API clients / small files); browser
// uploads use the presigned multipart protocol instead. Returns the object
// key. MIME/file-name failures are returned as invalidInputError.
func storeDirectUpload(ctx context.Context, toolType string, jobID string, fh *multipart.FileHeader) (string, error) {
	fileName := sanitizeFileName(fh.Filename)
	if fileName == "" {
		return "", invalidInputError{msg: "invalid file name"}
	}
	src, err := fh.Open()
	if err != nil {
		return "", fmt.Errorf("open multipart file: %w", err)
	}
	defer src.Close()

	head := make([]byte, sniffLen)
	n, err := io.ReadFull(src, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", fmt.Errorf("read multipart file head: %w", err)
	}
	head = head[:n]
	if err := validateMIMEHead(toolType, head); err != nil {
		return "", invalidInputError{msg: err.Error()}
	}

	key := fmt.Sprintf("uploads/%s/%s", jobID, fileName)
	body := io.MultiReader(bytes.NewReader(head), src)
	if err := objStore.PutObject(ctx, objStore.BucketUploads(), key, body, fh.Size, http.DetectContentType(head)); err != nil {
		return "", err
	}
	return key, nil
}

// bucketFor maps a FileMetadata kind to the bucket holding its object:
// inputs live in the uploads bucket, outputs in the outputs bucket.
func bucketFor(kind string) string {
	if kind == "input" {
		return objStore.BucketUploads()
	}
	return objStore.BucketOutputs()
}

func normalizeToolType(toolType string) (string, error) {
	toolType = strings.TrimSpace(toolType)
	switch toolType {
	case "ppt-to-pdf":
		return "powerpoint-to-pdf", nil
	case "pdf-to-ppt":
		return "pdf-to-powerpoint", nil
	case "pdf-to-img":
		return "pdf-to-image", nil
	case "img-to-pdf":
		return "image-to-pdf", nil
	}
	if toolType == "" {
		return "", fmt.Errorf("tool is required")
	}
	return toolType, nil
}

// jobResponse wraps ProcessingJob with a computed output file name so that
// API consumers do not have to replicate the extension-mapping logic.
type jobResponse struct {
	models.ProcessingJob
	OutputFileName string `json:"outputFileName"`
}

// createJobResponse extends jobResponse with an optional guest token so that
// cross-origin frontends can store the token and send it back via
// X-Guest-Token header on subsequent requests.
type createJobResponse struct {
	jobResponse
	GuestToken string `json:"guestToken,omitempty"`
}

func toJobResponse(job models.ProcessingJob) jobResponse {
	name, _ := outputFileName(job.ToolType, job.FileName, job.Metadata)
	return jobResponse{ProcessingJob: job, OutputFileName: name}
}

func toJobResponses(jobs []models.ProcessingJob) []jobResponse {
	out := make([]jobResponse, len(jobs))
	for i, j := range jobs {
		out[i] = toJobResponse(j)
	}
	return out
}

func outputFileName(toolType string, inputName string, metadata datatypes.JSON) (string, string) {
	contentType := "application/octet-stream"
	fileName := inputName
	switch toolType {
	case "pdf-to-word":
		fileName = strings.TrimSuffix(inputName, filepath.Ext(inputName)) + ".docx"
		contentType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case "pdf-to-excel":
		fileName = strings.TrimSuffix(inputName, filepath.Ext(inputName)) + ".xlsx"
		contentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case "pdf-to-powerpoint":
		fileName = strings.TrimSuffix(inputName, filepath.Ext(inputName)) + ".pptx"
		contentType = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case "pdf-to-image":
		ext, ct := resolveOutputExt(metadata, ".zip", "application/zip")
		fileName = strings.TrimSuffix(inputName, filepath.Ext(inputName)) + ext
		contentType = ct
	case "split-pdf", "pdf-to-html":
		fileName = strings.TrimSuffix(inputName, filepath.Ext(inputName)) + ".zip"
		contentType = "application/zip"
	case "pdf-to-text":
		fileName = strings.TrimSuffix(inputName, filepath.Ext(inputName)) + ".txt"
		contentType = "text/plain; charset=utf-8"
	case "pdf-to-odt", "word-to-odt":
		fileName = strings.TrimSuffix(inputName, filepath.Ext(inputName)) + ".odt"
		contentType = "application/vnd.oasis.opendocument.text"
	case "pdf-to-ods", "excel-to-ods":
		fileName = strings.TrimSuffix(inputName, filepath.Ext(inputName)) + ".ods"
		contentType = "application/vnd.oasis.opendocument.spreadsheet"
	case "pdf-to-odp", "powerpoint-to-odp":
		fileName = strings.TrimSuffix(inputName, filepath.Ext(inputName)) + ".odp"
		contentType = "application/vnd.oasis.opendocument.presentation"
	default:
		fileName = strings.TrimSuffix(inputName, filepath.Ext(inputName)) + ".pdf"
		contentType = "application/pdf"
	}
	return fileName, contentType
}

// resolveOutputExt reads the "outputExt" field from job metadata to determine
// the actual output file extension and content type. Falls back to the provided
// defaults if metadata is absent or does not contain the field.
func resolveOutputExt(metadata datatypes.JSON, defaultExt string, defaultCT string) (string, string) {
	if len(metadata) == 0 {
		return defaultExt, defaultCT
	}
	var m map[string]interface{}
	if err := json.Unmarshal(metadata, &m); err != nil {
		return defaultExt, defaultCT
	}
	ext, ok := m["outputExt"].(string)
	if !ok || ext == "" {
		return defaultExt, defaultCT
	}
	switch ext {
	case ".png":
		return ".png", "image/png"
	case ".jpg", ".jpeg":
		return ext, "image/jpeg"
	default:
		return defaultExt, defaultCT
	}
}

func maxUploadBytes() int64 {
	return config.MaxUploadBytes()
}

// planMaxFileSizeMB reads the per-plan file size limit from X-User-Plan-Max-File-MB.
// Falls back to the guest limit if the header is absent.
func planMaxFileSizeMB(c *gin.Context) int {
	val := c.GetHeader("X-User-Plan-Max-File-MB")
	if val == "" {
		return config.GuestMaxFileSizeMB()
	}
	mb, err := strconv.Atoi(val)
	if err != nil || mb <= 0 {
		return config.GuestMaxFileSizeMB()
	}
	return mb
}

// planMaxFilesPerJob reads the per-plan file count limit from X-User-Plan-Max-Files.
// Falls back to the guest limit if the header is absent.
func planMaxFilesPerJob(c *gin.Context) int {
	val := c.GetHeader("X-User-Plan-Max-Files")
	if val == "" {
		return config.GuestMaxFilesPerJob()
	}
	n, err := strconv.Atoi(val)
	if err != nil || n <= 0 {
		return config.GuestMaxFilesPerJob()
	}
	return n
}

func validateFileType(toolType string, fileName string) error {
	ext := strings.ToLower(filepath.Ext(fileName))
	isPDF := ext == ".pdf"
	switch toolType {
	case "pdf-to-word", "pdf-to-excel", "pdf-to-powerpoint", "pdf-to-image",
		"pdf-to-html", "pdf-to-text", "pdf-to-pdfa",
		"pdf-to-odt", "pdf-to-ods", "pdf-to-odp",
		"merge-pdf", "split-pdf", "compress-pdf",
		"rotate-pdf", "remove-pages", "extract-pages", "organize-pdf",
		"watermark-pdf", "protect-pdf", "unlock-pdf", "sign-pdf", "edit-pdf",
		"add-page-numbers", "repair-pdf", "ocr-pdf", "ocr":
		if !isPDF {
			return fmt.Errorf("only PDF files are supported for this tool")
		}
	case "scan-to-pdf":
		if ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".webp" && ext != ".pdf" {
			return fmt.Errorf("only image or PDF files are supported for this tool")
		}
	case "word-to-pdf", "word-to-odt":
		if ext != ".doc" && ext != ".docx" {
			return fmt.Errorf("only Word files are supported")
		}
	case "excel-to-pdf", "excel-to-ods":
		if ext != ".xls" && ext != ".xlsx" {
			return fmt.Errorf("only Excel files are supported")
		}
	case "powerpoint-to-pdf", "powerpoint-to-odp":
		if ext != ".ppt" && ext != ".pptx" {
			return fmt.Errorf("only PowerPoint files are supported")
		}
	case "image-to-pdf":
		if ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".webp" {
			return fmt.Errorf("only image files are supported")
		}
	case "odt-to-pdf":
		if ext != ".odt" {
			return fmt.Errorf("only ODT files are supported")
		}
	case "ods-to-pdf":
		if ext != ".ods" {
			return fmt.Errorf("only ODS files are supported")
		}
	case "odp-to-pdf":
		if ext != ".odp" {
			return fmt.Errorf("only ODP files are supported")
		}
	}
	return nil
}

// mimeCategory returns the MIME category key for the given tool type.
func mimeCategory(toolType string) string {
	switch toolType {
	case "pdf-to-word", "pdf-to-excel", "pdf-to-powerpoint", "pdf-to-image",
		"pdf-to-html", "pdf-to-text", "pdf-to-pdfa",
		"pdf-to-odt", "pdf-to-ods", "pdf-to-odp",
		"merge-pdf", "split-pdf", "compress-pdf",
		"rotate-pdf", "remove-pages", "extract-pages", "organize-pdf",
		"watermark-pdf", "protect-pdf", "unlock-pdf", "sign-pdf", "edit-pdf",
		"add-page-numbers", "repair-pdf", "ocr-pdf", "ocr":
		return "pdf"
	case "scan-to-pdf":
		return "image"
	case "word-to-pdf", "word-to-odt":
		return "word"
	case "excel-to-pdf", "excel-to-ods":
		return "excel"
	case "powerpoint-to-pdf", "powerpoint-to-odp":
		return "ppt"
	case "image-to-pdf":
		return "image"
	case "odt-to-pdf":
		return "odt"
	case "ods-to-pdf":
		return "ods"
	case "odp-to-pdf":
		return "odp"
	default:
		return ""
	}
}

// validateMIMEHead checks that the MIME type detected from the first bytes
// of a file (http.DetectContentType over up to 512 bytes) is in the allowlist
// for the given tool type. Callers pass the object's leading bytes — read via
// GetObjectRange for presigned uploads or teed off the multipart stream for
// direct uploads.
func validateMIMEHead(toolType string, head []byte) error {
	category := mimeCategory(toolType)
	if category == "" {
		return nil // unknown category, skip MIME check
	}
	allowed, ok := allowedMIMETypes[category]
	if !ok {
		return nil
	}

	if len(head) > sniffLen {
		head = head[:sniffLen]
	}
	detected := http.DetectContentType(head)

	for _, a := range allowed {
		if detected == a {
			return nil
		}
	}
	return fmt.Errorf("file content type %q is not allowed for this tool", detected)
}

func parseOptions(raw string) map[string]interface{} {
	if raw == "" {
		return map[string]interface{}{}
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return map[string]interface{}{}
	}
	return parsed
}

func optionsPayload(raw string) json.RawMessage {
	if raw == "" {
		return nil
	}
	if !json.Valid([]byte(raw)) {
		return nil
	}
	return json.RawMessage(raw)
}

func queryInt(c *gin.Context, key string, fallback int) int {
	value := c.Query(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func clampInt(value int, min int, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// jobExpiry returns when a job (and its input+output files) should be deleted.
// Retention is per-plan and env-driven (GUEST_JOB_TTL / FREE_JOB_TTL /
// PRO_JOB_TTL); the canonical fallbacks live in fyredocs/shared/config so the
// cleanup binary can never drift from what job-service promises. Every job
// gets a finite expiry — including pro — so cleanup fully governs deletion and
// no job is left to linger forever.
func jobExpiry(userID *uuid.UUID, planName string) *time.Time {
	var ttl time.Duration
	switch {
	case userID == nil:
		ttl = config.GuestJobTTL()
	case planName == "pro":
		ttl = config.ProJobTTL()
	default:
		ttl = config.FreeJobTTL()
	}
	expires := time.Now().UTC().Add(ttl)
	return &expires
}

func assignGuestTokenIfNeeded(c *gin.Context, userID *uuid.UUID, jobID uuid.UUID) string {
	if userID != nil {
		return ""
	}
	if redisstore.Client == nil {
		return ""
	}
	token := guestToken(c)
	if token == "" {
		token = uuid.NewString()
	}
	ctx := c.Request.Context()
	key := fmt.Sprintf("guest:%s:jobs", token)
	redisstore.Client.SAdd(ctx, key, jobID.String())
	redisstore.Client.Expire(ctx, key, config.GuestJobTTL())
	c.SetCookie("guest_token", token, int(config.GuestJobTTL().Seconds()), "/", "", false, true)
	return token
}

func guestJobIDs(ctx context.Context, token string) []string {
	if token == "" || redisstore.Client == nil {
		return nil
	}
	key := fmt.Sprintf("guest:%s:jobs", token)
	jobIDs, err := redisstore.Client.SMembers(ctx, key).Result()
	if err != nil {
		return nil
	}
	return jobIDs
}

func removeGuestJob(ctx context.Context, token string, jobID uuid.UUID) {
	if ctx == nil || redisstore.Client == nil {
		return
	}
	if token == "" {
		return
	}
	key := fmt.Sprintf("guest:%s:jobs", token)
	redisstore.Client.SRem(ctx, key, jobID.String())
}

func authorizeJobAccess(c *gin.Context, job *models.ProcessingJob) bool {
	userID := authUserID(c)
	if job.UserID != nil {
		if userID == nil {
			return false
		}
		return job.UserID.String() == userID.String()
	}
	token := guestToken(c)
	if token == "" {
		return false
	}
	for _, id := range guestJobIDs(c.Request.Context(), token) {
		if id == job.ID.String() {
			return true
		}
	}
	return false
}

func guestToken(c *gin.Context) string {
	if value, err := c.Cookie("guest_token"); err == nil {
		return value
	}
	return ""
}

func publishJobAnalyticsEvent(ctx context.Context, eventType string, jobID string, toolType string, userID *uuid.UUID, fileSize int64) {
	if natsconn.JS == nil {
		return
	}
	event := queue.AnalyticsEvent{
		EventType: eventType,
		JobID:     jobID,
		ToolType:  toolType,
		FileSize:  fileSize,
		IsGuest:   userID == nil,
		Timestamp: time.Now().UTC(),
	}
	if userID != nil {
		event.UserID = userID.String()
	}
	if err := queue.PublishAnalyticsEvent(ctx, natsconn.JS, event); err != nil {
		slog.Warn("failed to publish analytics event", "eventType", eventType, "error", err)
	}
}

func publishPlanLimitHit(c *gin.Context, limitType string, toolType string) {
	if natsconn.JS == nil {
		return
	}
	userID := authUserID(c)
	event := queue.AnalyticsEvent{
		EventType: "plan.limit_hit",
		ToolType:  toolType,
		IsGuest:   userID == nil,
		Timestamp: time.Now().UTC(),
		Metadata:  json.RawMessage(fmt.Sprintf(`{"limitType":%q}`, limitType)),
	}
	if userID != nil {
		event.UserID = userID.String()
	}
	if err := queue.PublishAnalyticsEvent(c.Request.Context(), natsconn.JS, event); err != nil {
		slog.Warn("failed to publish plan limit event", "limitType", limitType, "error", err)
	}
}

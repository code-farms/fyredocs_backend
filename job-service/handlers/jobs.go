package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
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

	"fyredocs/shared/natsconn"
	"fyredocs/shared/queue"
	"fyredocs/shared/redisstore"
	"fyredocs/shared/response"

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

type UploadJobRequest struct {
	UploadID  string          `json:"uploadId"`
	UploadIDs []string        `json:"uploadIds"`
	Options   json.RawMessage `json:"options"`
}

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

	// Idempotency: check for Idempotency-Key header
	idempotencyKey := c.GetHeader("Idempotency-Key")
	if idempotencyKey != "" && redisstore.Client != nil {
		redisKey := fmt.Sprintf("idempotency:%s", idempotencyKey)
		existing, err := redisstore.Client.Get(c.Request.Context(), redisKey).Result()
		if err == nil && existing != "" {
			var existingJob models.ProcessingJob
			if err := models.DB.First(&existingJob, "id = ?", existing).Error; err == nil {
				guestTok := assignGuestTokenIfNeeded(c, authUserID(c), existingJob.ID)
				response.Created(c, "Your file is being processed!", createJobResponse{
					jobResponse: toJobResponse(existingJob),
					GuestToken:  guestTok,
				})
				return
			}
		}
	}

	jobID := uuid.New()
	uploadDir := uploadBaseDir()
	jobDir := filepath.Join(uploadDir, jobID.String())
	// Fix #17: Use 0750 instead of os.ModePerm
	if err := os.MkdirAll(jobDir, 0750); err != nil {
		response.InternalError(c, "SERVER_ERROR", "Something went wrong. Please try again.")
		return
	}

	jobCreated := false
	defer func() {
		if !jobCreated {
			_ = os.RemoveAll(jobDir)
		}
	}()

	var totalSize int64
	var inputPaths []string
	var fileMetas []models.FileMetadata
	var consumedUploadIDs []string
	originalName := ""
	optionsRaw := ""

	if strings.Contains(c.GetHeader("Content-Type"), "application/json") {
		var uploadReq UploadJobRequest
		if err := c.ShouldBindJSON(&uploadReq); err != nil {
			response.BadRequest(c, "INVALID_INPUT", "Invalid request. Please try again.")
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

		optionsRaw = string(uploadReq.Options)

		for idx, uploadID := range uploadIDs {
			consumed, size, err := consumeUpload(c.Request.Context(), toolType, uploadID, jobDir, idx)
			if err != nil {
				response.BadRequest(c, "INVALID_INPUT", err.Error())
				return
			}
			consumedUploadIDs = append(consumedUploadIDs, uploadID)
			if err := validateMIMEType(toolType, consumed.Path); err != nil {
				response.BadRequest(c, "INVALID_INPUT", err.Error())
				return
			}
			totalSize += size
			inputPaths = append(inputPaths, consumed.Path)
			fileMetas = append(fileMetas, models.FileMetadata{
				ID:           uuid.New(),
				JobID:        jobID,
				Kind:         "input",
				OriginalName: consumed.OriginalName,
				Path:         consumed.Path,
				SizeBytes:    size,
			})
			if originalName == "" {
				originalName = consumed.OriginalName
			}
		}
	} else {
		form, err := c.MultipartForm()
		if err != nil {
			response.BadRequest(c, "INVALID_INPUT", "Invalid file upload. Please try again.")
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
				response.BadRequest(c, "INVALID_INPUT", err.Error())
				return
			}
			dst := filepath.Join(jobDir, filepath.Base(file.Filename))
			if err := c.SaveUploadedFile(file, dst); err != nil {
				response.InternalError(c, "SERVER_ERROR", "Something went wrong. Please try again.")
				return
			}
			if err := validateMIMEType(toolType, dst); err != nil {
				response.BadRequest(c, "INVALID_INPUT", err.Error())
				return
			}
			totalSize += file.Size
			inputPaths = append(inputPaths, dst)
			fileMetas = append(fileMetas, models.FileMetadata{
				ID:           uuid.New(),
				JobID:        jobID,
				Kind:         "input",
				OriginalName: file.Filename,
				Path:         dst,
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
		response.InternalError(c, "SERVER_ERROR", "Something went wrong. Please try again.")
		return
	}

	// Fix #12: Progress is now int, FileSize is now int64
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

	if err := models.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&job).Error; err != nil {
			return err
		}
		for _, meta := range fileMetas {
			if err := tx.Create(&meta).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		response.InternalError(c, "SERVER_ERROR", "Something went wrong. Please try again.")
		return
	}

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
		response.InternalError(c, "SERVER_ERROR", "Our servers are busy. Please try again in a moment.")
		return
	}

	jobCreated = true

	// Free the upload slot only after the job is fully committed and queued.
	// On any failure above this line, the upload state is preserved so the
	// frontend can retry with the same uploadId without re-uploading.
	for _, id := range consumedUploadIDs {
		releaseUpload(c.Request.Context(), id)
	}

	// Store idempotency key in Redis with 10-minute TTL
	if idempotencyKey != "" && redisstore.Client != nil {
		redisKey := fmt.Sprintf("idempotency:%s", idempotencyKey)
		redisstore.Client.Set(c.Request.Context(), redisKey, job.ID.String(), 10*time.Minute)
	}

	publishJobAnalyticsEvent(c.Request.Context(), "job.created", toolType, userID, totalSize)
	slog.Info("job queued", "jobId", job.ID, "tool", toolType, "correlationId", correlationID)
	response.Created(c, "Your file is being processed!", createJobResponse{
		jobResponse: toJobResponse(job),
		GuestToken:  guestTok,
	})
}

// Fix #29: Add pagination to GetJobsByTool
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
			response.InternalError(c, "SERVER_ERROR", "Could not load your jobs. Please try again.")
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
		response.InternalError(c, "SERVER_ERROR", "Could not load your jobs. Please try again.")
		return
	}
	response.OKWithMeta(c, "Jobs loaded successfully", toJobResponses(jobs), &response.Meta{Page: page, Limit: limit})
}

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
		if err := os.Remove(file.Path); err != nil {
			slog.Warn("failed to remove file", "path", file.Path, "error", err)
		}
	}
	if err := models.DB.Where("job_id = ?", job.ID).Delete(&models.FileMetadata{}).Error; err != nil {
		slog.Error("failed to delete file metadata", "jobId", job.ID, "error", err)
	}

	if err := models.DB.Delete(&job).Error; err != nil {
		response.InternalError(c, "SERVER_ERROR", "Could not delete the job. Please try again.")
		return
	}

	removeGuestJob(c.Request.Context(), guestToken(c), job.ID)
	response.NoContent(c)
}

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
	// Fix #6: Use mime.FormatMediaType for safe Content-Disposition
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": fileName}))
	c.Header("Content-Type", contentType)
	c.File(outputFile.Path)
}

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
		response.InternalError(c, "SERVER_ERROR", "Could not load your job history. Please try again.")
		return
	}

	response.OKWithMeta(c, "Job history loaded", toJobResponses(jobs), &response.Meta{Page: page, Limit: limit})
}

type consumedUpload struct {
	Path         string
	OriginalName string
}

// consumeUpload materialises an uploaded file into jobDir without removing the
// source. The Redis state and original upload directory are intentionally left
// in place so the caller can retry on a downstream failure (MIME validation,
// DB transaction, queue publish) without forcing the user to re-upload. The
// caller must invoke releaseUpload after the job is committed to free the
// upload slot.
func consumeUpload(ctx context.Context, toolType string, uploadID string, jobDir string, index int) (consumedUpload, int64, error) {
	if uploadID == "" {
		return consumedUpload{}, 0, fmt.Errorf("uploadId is required")
	}
	if redisstore.Client == nil {
		return consumedUpload{}, 0, fmt.Errorf("redis unavailable")
	}
	state, err := redisstore.Client.HGetAll(ctx, uploadStateKey(uploadID)).Result()
	if err != nil {
		if err == redis.Nil {
			return consumedUpload{}, 0, fmt.Errorf("upload not found")
		}
		return consumedUpload{}, 0, fmt.Errorf("failed to read upload state")
	}
	if len(state) == 0 {
		return consumedUpload{}, 0, fmt.Errorf("upload not found")
	}
	fileName := state["fileName"]
	if fileName == "" {
		return consumedUpload{}, 0, fmt.Errorf("upload file missing")
	}
	if err := validateFileType(toolType, fileName); err != nil {
		return consumedUpload{}, 0, err
	}
	sourcePath := filepath.Join(uploadBaseDir(), uploadID, fileName)
	if _, err := os.Stat(sourcePath); err != nil {
		return consumedUpload{}, 0, fmt.Errorf("assembled file missing")
	}

	destName := uniqueUploadFileName(uploadID, fileName, index)
	destPath := filepath.Join(jobDir, destName)
	if err := linkOrCopyFile(sourcePath, destPath); err != nil {
		return consumedUpload{}, 0, fmt.Errorf("failed to move upload")
	}
	info, err := os.Stat(destPath)
	if err != nil {
		return consumedUpload{}, 0, err
	}
	if info.Size() > maxUploadBytes() {
		return consumedUpload{}, 0, fmt.Errorf("file exceeds maximum size")
	}

	return consumedUpload{Path: destPath, OriginalName: fileName}, info.Size(), nil
}

// releaseUpload clears the Redis state and removes the source upload directory
// for a successfully consumed upload. Failures are logged but never returned —
// the job has already been queued and a stuck upload record will be cleaned up
// by the cleanup-worker / TTL.
func releaseUpload(ctx context.Context, uploadID string) {
	if uploadID == "" || redisstore.Client == nil {
		return
	}
	if err := redisstore.Client.Del(ctx, uploadStateKey(uploadID), uploadStateKey(uploadID)+":chunks").Err(); err != nil {
		slog.Warn("failed to clear upload state", "uploadId", uploadID, "error", err)
	}
	if err := os.RemoveAll(filepath.Join(uploadBaseDir(), uploadID)); err != nil {
		slog.Warn("failed to remove upload directory", "uploadId", uploadID, "error", err)
	}
}

func uniqueUploadFileName(uploadID string, fileName string, index int) string {
	base := filepath.Base(fileName)
	if uploadID == "" {
		return base
	}
	return fmt.Sprintf("%s_%d_%s", uploadID, index, base)
}

// linkOrCopyFile materialises src at dst without removing src. It first tries
// a hardlink (zero-copy on the same filesystem) and falls back to a byte copy
// when hardlinking is not supported (cross-device, EXDEV, or filesystems that
// disallow links). Leaving src intact is what makes the upload retry-safe.
func linkOrCopyFile(src string, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}

	copied := false
	defer func() {
		_ = out.Close()
		if !copied {
			_ = os.Remove(dst)
		}
	}()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	copied = true
	return nil
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
	value := os.Getenv("MAX_UPLOAD_MB")
	if value == "" {
		return 50 * 1024 * 1024
	}
	mb, err := strconv.Atoi(value)
	if err != nil || mb <= 0 {
		return 50 * 1024 * 1024
	}
	return int64(mb) * 1024 * 1024
}

// planMaxFileSizeMB reads the per-plan file size limit from X-User-Plan-Max-File-MB.
// Falls back to 10MB (anonymous limit) if the header is absent.
func planMaxFileSizeMB(c *gin.Context) int {
	val := c.GetHeader("X-User-Plan-Max-File-MB")
	if val == "" {
		return 10
	}
	mb, err := strconv.Atoi(val)
	if err != nil || mb <= 0 {
		return 10
	}
	return mb
}

// planMaxFilesPerJob reads the per-plan file count limit from X-User-Plan-Max-Files.
// Falls back to 5 (anonymous limit) if the header is absent.
func planMaxFilesPerJob(c *gin.Context) int {
	val := c.GetHeader("X-User-Plan-Max-Files")
	if val == "" {
		return 5
	}
	n, err := strconv.Atoi(val)
	if err != nil || n <= 0 {
		return 5
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

// validateMIMEType reads the first 512 bytes of the file and checks that
// the detected MIME type is in the allowlist for the given tool type.
func validateMIMEType(toolType string, filePath string) error {
	category := mimeCategory(toolType)
	if category == "" {
		return nil // unknown category, skip MIME check
	}
	allowed, ok := allowedMIMETypes[category]
	if !ok {
		return nil
	}

	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file for MIME detection")
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return fmt.Errorf("failed to read file for MIME detection")
	}
	detected := http.DetectContentType(buf[:n])

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

func jobExpiry(userID *uuid.UUID, planName string) *time.Time {
	if userID == nil {
		ttl := guestJobTTL()
		expires := time.Now().UTC().Add(ttl)
		return &expires
	}
	if planName == "pro" {
		return nil
	}
	ttl := freeJobTTL()
	expires := time.Now().UTC().Add(ttl)
	return &expires
}

func guestJobTTL() time.Duration {
	value := os.Getenv("GUEST_JOB_TTL")
	if value == "" {
		return 30 * time.Minute
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 30 * time.Minute
	}
	return parsed
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
	redisstore.Client.Expire(ctx, key, guestJobTTL())
	c.SetCookie("guest_token", token, int(guestJobTTL().Seconds()), "/", "", false, true)
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

func publishJobAnalyticsEvent(ctx context.Context, eventType string, toolType string, userID *uuid.UUID, fileSize int64) {
	if natsconn.JS == nil {
		return
	}
	event := queue.AnalyticsEvent{
		EventType: eventType,
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

package handlers

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"document-service/internal/models"
	"document-service/internal/notifyclient"
	"fyredocs/shared/config"
	"fyredocs/shared/logger"
	"fyredocs/shared/response"
)

// Export generation is bounded: a semaphore caps how many exports render
// concurrently (each does a full library scan + in-memory artifact build), and
// exportDocs caps rows. Without this a burst of export requests spawns unbounded
// goroutines that each buffer a large artifact in memory → OOM/DoS.
var (
	exportSemOnce sync.Once
	exportSem     chan struct{}
)

// acquireExportSlot takes one of EXPORT_MAX_CONCURRENCY (default 4) export slots,
// blocking until one frees or ctx is cancelled. Returns false only on
// cancellation.
//
// This is the DoS bound described above: each export scans a user's entire library
// and builds the artifact in memory, so without a cap a burst of requests spawns
// unbounded concurrent buffers and OOMs the service. The semaphore is created
// under sync.Once on first use rather than at init so the environment is read
// after configuration has loaded.
//
// Every successful call MUST be paired with releaseExportSlot, conventionally via
// defer — a leaked slot is permanent and shrinks capacity for the process's life.
func acquireExportSlot(ctx context.Context) bool {
	exportSemOnce.Do(func() {
		n := config.GetEnvInt("EXPORT_MAX_CONCURRENCY", 4)
		if n < 1 {
			n = 1
		}
		exportSem = make(chan struct{}, n)
	})
	select {
	case exportSem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// releaseExportSlot returns a slot. Panics if called without a matching
// acquireExportSlot having initialised the semaphore, which would be a
// programming error rather than a runtime condition.
func releaseExportSlot() { <-exportSem }

// exportFilters narrows which documents an export includes. Stored on the export
// row so a re-run reproduces the same selection.
type exportFilters struct {
	Status   string `json:"status,omitempty"`
	FolderID string `json:"folderId,omitempty"`
	TagID    string `json:"tagId,omitempty"`
}

// createExportReq is the export request body.
type createExportReq struct {
	// Format is validated case-insensitively by the handler (csv/json), so the
	// tag only bounds it.
	Format         string  `json:"format" binding:"required,max=10"`
	OrganizationID *string `json:"organizationId" binding:"omitempty,uuid"`
	Status         string  `json:"status" binding:"omitempty,max=32"`
	FolderID       string  `json:"folderId" binding:"omitempty,uuid"`
	TagID          string  `json:"tagId" binding:"omitempty,uuid"`
}

// CreateExport queues an async export of the caller's documents (current scope
// + filters). Reading org documents requires viewer+.
func CreateExport(c *gin.Context) {
	uid, _ := userID(c)
	var req createExportReq
	if !response.BindJSON(c, &req) {
		return
	}
	format := strings.ToLower(strings.TrimSpace(req.Format))
	if format != "csv" && format != "json" {
		response.BadRequest(c, "INVALID_FORMAT", "Format must be csv or json.")
		return
	}
	orgIDStr := ""
	if req.OrganizationID != nil {
		orgIDStr = *req.OrganizationID
	}
	orgID, ok := resolveOrg(c, orgIDStr, "viewer")
	if !ok {
		return
	}
	filters, _ := json.Marshal(exportFilters{Status: req.Status, FolderID: req.FolderID, TagID: req.TagID})
	exp := models.Export{UserID: uid, OrganizationID: orgID, Format: format, Status: models.ExportQueued, Filters: filters}
	if err := models.DB.Create(&exp).Error; err != nil {
		response.InternalErrorf(c, response.CodeServerError, "Could not create export.", err,
			"op", "db.exports.create", "userId", uid)
		return
	}
	go generateExport(exp.ID)
	response.Created(c, "Export queued", exp)
}

// ListExports returns the caller's exports (newest first), without artifact bytes.
func ListExports(c *gin.Context) {
	uid, _ := userID(c)
	var exports []models.Export
	if err := models.DB.Omit("Content").Where("user_id = ?", uid).Order("created_at DESC").Limit(50).Find(&exports).Error; err != nil {
		response.InternalErrorf(c, response.CodeServerError, "Could not load exports.", err, "op", "db.exports.list", "userId", uid)
		return
	}
	response.OK(c, "Exports retrieved", exports)
}

// GetExport returns one export's status/metadata.
func GetExport(c *gin.Context) {
	uid, _ := userID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, response.CodeInvalidID, "Invalid export id.")
		return
	}
	var exp models.Export
	if err := models.DB.Omit("Content").Where("id = ? AND user_id = ?", id, uid).First(&exp).Error; err != nil {
		response.NotFound(c, response.CodeNotFound, "Export not found.")
		return
	}
	response.OK(c, "Export retrieved", exp)
}

// DownloadExport streams a ready export's artifact.
func DownloadExport(c *gin.Context) {
	uid, _ := userID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, response.CodeInvalidID, "Invalid export id.")
		return
	}
	var exp models.Export
	if err := models.DB.Where("id = ? AND user_id = ?", id, uid).First(&exp).Error; err != nil {
		response.NotFound(c, response.CodeNotFound, "Export not found.")
		return
	}
	if exp.Status != models.ExportReady || len(exp.Content) == 0 {
		response.Err(c, http.StatusConflict, "NOT_READY", "Export is not ready yet.")
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", exp.FileName))
	c.Data(http.StatusOK, exp.ContentType, exp.Content)
}

// generateExport runs asynchronously: query the documents in scope, render the
// artifact, and store it on the export row.
func generateExport(id uuid.UUID) {
	// Detached worker: the request context is gone once CreateExport responded, so
	// use a fresh background context. Logs still carry service + op + exportId.
	ctx := context.Background()
	defer func() {
		if r := recover(); r != nil {
			logger.LogErr(ctx, "export.generate.panic", fmt.Errorf("panic: %v", r), "exportId", id)
			models.DB.Model(&models.Export{}).Where("id = ?", id).
				Updates(map[string]any{"status": models.ExportFailed, "error": fmt.Sprintf("panic: %v", r)})
		}
	}()

	// Bound concurrent export work. Wait up to EXPORT_QUEUE_WAIT for a slot; if
	// the queue stays saturated, fail the export with a retryable message rather
	// than piling up goroutines that each buffer a large artifact.
	waitCtx, cancel := context.WithTimeout(ctx, config.GetEnvDuration("EXPORT_QUEUE_WAIT", 30*time.Second))
	defer cancel()
	if !acquireExportSlot(waitCtx) {
		logger.LogWarn(ctx, "export.queue_saturated", fmt.Errorf("no export slot available"), "exportId", id)
		models.DB.Model(&models.Export{}).Where("id = ?", id).
			Updates(map[string]any{"status": models.ExportFailed, "error": "export queue is busy, please retry"})
		return
	}
	defer releaseExportSlot()

	var exp models.Export
	if err := models.DB.First(&exp, "id = ?", id).Error; err != nil {
		logger.LogErr(ctx, "db.exports.load", err, "exportId", id)
		return
	}
	slog.InfoContext(ctx, "export started", "op", "export.generate", "exportId", id, "format", exp.Format, "userId", exp.UserID)
	if err := models.DB.Model(&models.Export{}).Where("id = ?", id).Update("status", models.ExportProcessing).Error; err != nil {
		logger.LogWarn(ctx, "db.exports.update_status", err, "exportId", id)
	}

	docs, err := exportDocs(exp)
	if err != nil {
		logger.LogErr(ctx, "export.query_docs", err, "exportId", id)
		if uerr := models.DB.Model(&models.Export{}).Where("id = ?", id).
			Updates(map[string]any{"status": models.ExportFailed, "error": err.Error()}).Error; uerr != nil {
			logger.LogWarn(ctx, "db.exports.mark_failed", uerr, "exportId", id)
		}
		return
	}

	var content []byte
	var contentType, ext string
	switch exp.Format {
	case "json":
		if content, err = json.MarshalIndent(docsToJSON(docs), "", "  "); err != nil {
			logger.LogErr(ctx, "export.marshal", err, "exportId", id)
			models.DB.Model(&models.Export{}).Where("id = ?", id).
				Updates(map[string]any{"status": models.ExportFailed, "error": err.Error()})
			return
		}
		contentType, ext = "application/json", "json"
	default:
		content = docsToCSV(docs)
		contentType, ext = "text/csv", "csv"
	}

	now := time.Now().UTC()
	fileName := fmt.Sprintf("documents-%s.%s", now.Format("20060102-150405"), ext)
	if err := models.DB.Model(&models.Export{}).Where("id = ?", id).Updates(map[string]any{
		"status":         models.ExportReady,
		"content":        content,
		"content_type":   contentType,
		"file_name":      fileName,
		"document_count": len(docs),
		"completed_at":   now,
	}).Error; err != nil {
		logger.LogErr(ctx, "db.exports.finalize", err, "exportId", id)
		return
	}
	slog.InfoContext(ctx, "export completed", "op", "export.generate", "exportId", id, "documentCount", len(docs))

	// Best-effort: raise an in-app notification (idempotent on the export id).
	notifyclient.Notify(ctx, exp.UserID.String(), "export.ready",
		"Export ready", fileName, "/app/exports", id.String())
}

// exportDocs loads the documents for an export, capped in row count.
//
// The cap is a memory bound, not a product limit: the result set is serialized
// into an in-memory buffer, so an unbounded library would decide this service's
// peak memory. A user at the cap gets a truncated export rather than a failure.
func exportDocs(exp models.Export) ([]models.Document, error) {
	var f exportFilters
	_ = json.Unmarshal(exp.Filters, &f)

	q := scopeOwned(models.DB.Preload("Tags"), exp.UserID, exp.OrganizationID)
	if f.Status != "" {
		q = q.Where("documents.status = ?", f.Status)
	}
	if f.FolderID != "" {
		if fid, err := uuid.Parse(f.FolderID); err == nil {
			q = q.Where("documents.folder_id = ?", fid)
		}
	}
	if f.TagID != "" {
		if tid, err := uuid.Parse(f.TagID); err == nil {
			q = q.Joins("JOIN document_tags dt ON dt.document_id = documents.id").Where("dt.tag_id = ?", tid)
		}
	}
	// Cap rows so a huge library can't be buffered into memory (the artifact is
	// built fully in RAM and stored on the export row).
	maxRows := config.GetEnvInt("EXPORT_MAX_ROWS", 50000)
	if maxRows < 1 {
		maxRows = 50000
	}
	var docs []models.Document
	err := q.Order("documents.created_at DESC").Limit(maxRows).Find(&docs).Error
	return docs, err
}

// tagNames flattens a document's tags to their names for the export columns.
func tagNames(d models.Document) string {
	names := make([]string, 0, len(d.Tags))
	for _, t := range d.Tags {
		names = append(names, t.Name)
	}
	return strings.Join(names, "; ")
}

// docsToCSV renders documents as CSV, built in memory — see exportDocs for why
// the row count must stay bounded.
func docsToCSV(docs []models.Document) []byte {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"Name", "Type", "Size (bytes)", "Status", "Created", "Tags"})
	for _, d := range docs {
		_ = w.Write([]string{
			d.Name, d.FileType, strconv.FormatInt(d.FileSize, 10), d.Status,
			d.CreatedAt.Format(time.RFC3339), tagNames(d),
		})
	}
	w.Flush()
	return buf.Bytes()
}

// docsToJSON renders documents as JSON, with the same in-memory constraint as
// docsToCSV.
func docsToJSON(docs []models.Document) []map[string]any {
	out := make([]map[string]any, 0, len(docs))
	for _, d := range docs {
		names := make([]string, 0, len(d.Tags))
		for _, t := range d.Tags {
			names = append(names, t.Name)
		}
		out = append(out, map[string]any{
			"id": d.ID, "name": d.Name, "fileType": d.FileType, "fileSize": d.FileSize,
			"status": d.Status, "createdAt": d.CreatedAt, "tags": names,
		})
	}
	return out
}

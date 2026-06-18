package handlers

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"document-service/internal/models"
	"document-service/internal/notifyclient"
	"fyredocs/shared/response"
)

type exportFilters struct {
	Status   string `json:"status,omitempty"`
	FolderID string `json:"folderId,omitempty"`
	TagID    string `json:"tagId,omitempty"`
}

type createExportReq struct {
	Format         string  `json:"format"`
	OrganizationID *string `json:"organizationId"`
	Status         string  `json:"status"`
	FolderID       string  `json:"folderId"`
	TagID          string  `json:"tagId"`
}

// CreateExport queues an async export of the caller's documents (current scope
// + filters). Reading org documents requires viewer+.
func CreateExport(c *gin.Context) {
	uid, _ := userID(c)
	var req createExportReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_BODY", "Invalid request body.")
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
		response.InternalError(c, "CREATE_FAILED", "Could not create export.")
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
		response.InternalError(c, "LIST_FAILED", "Could not load exports.")
		return
	}
	response.OK(c, "Exports retrieved", exports)
}

// GetExport returns one export's status/metadata.
func GetExport(c *gin.Context) {
	uid, _ := userID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid export id.")
		return
	}
	var exp models.Export
	if err := models.DB.Omit("Content").Where("id = ? AND user_id = ?", id, uid).First(&exp).Error; err != nil {
		response.NotFound(c, "NOT_FOUND", "Export not found.")
		return
	}
	response.OK(c, "Export retrieved", exp)
}

// DownloadExport streams a ready export's artifact.
func DownloadExport(c *gin.Context) {
	uid, _ := userID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid export id.")
		return
	}
	var exp models.Export
	if err := models.DB.Where("id = ? AND user_id = ?", id, uid).First(&exp).Error; err != nil {
		response.NotFound(c, "NOT_FOUND", "Export not found.")
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
	var exp models.Export
	if err := models.DB.First(&exp, "id = ?", id).Error; err != nil {
		return
	}
	models.DB.Model(&models.Export{}).Where("id = ?", id).Update("status", models.ExportProcessing)

	docs, err := exportDocs(exp)
	if err != nil {
		models.DB.Model(&models.Export{}).Where("id = ?", id).Updates(map[string]any{"status": models.ExportFailed, "error": err.Error()})
		return
	}

	var content []byte
	var contentType, ext string
	switch exp.Format {
	case "json":
		content, _ = json.MarshalIndent(docsToJSON(docs), "", "  ")
		contentType, ext = "application/json", "json"
	default:
		content = docsToCSV(docs)
		contentType, ext = "text/csv", "csv"
	}

	now := time.Now().UTC()
	fileName := fmt.Sprintf("documents-%s.%s", now.Format("20060102-150405"), ext)
	models.DB.Model(&models.Export{}).Where("id = ?", id).Updates(map[string]any{
		"status":         models.ExportReady,
		"content":        content,
		"content_type":   contentType,
		"file_name":      fileName,
		"document_count": len(docs),
		"completed_at":   now,
	})

	// Best-effort: raise an in-app notification (idempotent on the export id).
	notifyclient.Notify(context.Background(), exp.UserID.String(), "export.ready",
		"Export ready", fileName, "/app/exports", id.String())
}

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
	var docs []models.Document
	err := q.Order("documents.created_at DESC").Find(&docs).Error
	return docs, err
}

func tagNames(d models.Document) string {
	names := make([]string, 0, len(d.Tags))
	for _, t := range d.Tags {
		names = append(names, t.Name)
	}
	return strings.Join(names, "; ")
}

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

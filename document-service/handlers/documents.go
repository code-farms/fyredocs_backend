package handlers

import (
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"document-service/internal/models"
	"document-service/internal/orgclient"
	"fyredocs/shared/response"
)

// resolveOrg determines a request's scope. With no orgId it returns (nil, true)
// meaning personal scope. With an orgId it verifies the caller's membership via
// user-service and that their role meets minRole; on failure it writes the
// error response and returns ok=false.
func resolveOrg(c *gin.Context, orgIDStr, minRole string) (*uuid.UUID, bool) {
	orgIDStr = strings.TrimSpace(orgIDStr)
	if orgIDStr == "" {
		return nil, true
	}
	orgID, err := uuid.Parse(orgIDStr)
	if err != nil {
		response.BadRequest(c, "INVALID_ORG", "Invalid organization id.")
		return nil, false
	}
	uid, _ := userID(c)
	role, member, err := orgclient.Membership(c.Request.Context(), orgID.String(), uid.String())
	if err != nil {
		response.Err(c, http.StatusBadGateway, "ORG_CHECK_FAILED", "Could not verify organization access.")
		return nil, false
	}
	if !member {
		response.NotFound(c, "NOT_FOUND", "Organization not found.")
		return nil, false
	}
	if !roleAtLeast(role, minRole) {
		response.Forbidden(c, "FORBIDDEN", "You don't have permission for this action.")
		return nil, false
	}
	return &orgID, true
}

// scopeOwned applies row ownership to a query: org documents (any member, per
// role already checked) or the caller's personal documents.
func scopeOwned(q *gorm.DB, uid uuid.UUID, orgID *uuid.UUID) *gorm.DB {
	if orgID != nil {
		return q.Where("organization_id = ?", *orgID)
	}
	return q.Where("user_id = ? AND organization_id IS NULL", uid)
}

// ListDocuments returns the caller's documents with optional filters:
// status, folderId, tagId, and full-text q. Cursor-friendly page/limit paging.
func ListDocuments(c *gin.Context) {
	uid, _ := userID(c)
	orgID, ok := resolveOrg(c, c.Query("orgId"), "viewer")
	if !ok {
		return
	}
	limit := queryInt(c, "limit", 25)
	if limit > 100 {
		limit = 100
	}
	page := queryInt(c, "page", 1)
	offset := (page - 1) * limit

	// Trash view lists soft-deleted documents (excluded from normal queries).
	trashed := c.Query("trashed") == "true"

	// Read filters into locals up front: the two query builders below run on
	// separate goroutines, and gin.Context's lazy form parsing is not safe for
	// concurrent access.
	status := strings.TrimSpace(c.Query("status"))
	folder := strings.TrimSpace(c.Query("folderId"))
	tagID := strings.TrimSpace(c.Query("tagId"))
	search := strings.TrimSpace(c.Query("q"))

	// buildQuery returns a fresh, fully-filtered query. Each call builds an
	// independent *gorm.DB statement, so the count and the page fetch can run
	// concurrently without sharing (and racing on) one statement.
	buildQuery := func() *gorm.DB {
		base := models.DB.Model(&models.Document{})
		if trashed {
			base = models.DB.Unscoped().Model(&models.Document{})
		}
		q := scopeOwned(base, uid, orgID)
		if trashed {
			q = q.Where("documents.deleted_at IS NOT NULL")
		}
		if status != "" {
			q = q.Where("documents.status = ?", status)
		}
		if folder != "" {
			if fid, err := uuid.Parse(folder); err == nil {
				q = q.Where("documents.folder_id = ?", fid)
			}
		}
		if tagID != "" {
			if tid, err := uuid.Parse(tagID); err == nil {
				q = q.Joins("JOIN document_tags dt ON dt.document_id = documents.id").Where("dt.tag_id = ?", tid)
			}
		}
		if search != "" {
			q = q.Where("documents.search_vector @@ websearch_to_tsquery('english', ?)", search)
		}
		return q
	}

	// The total count and the page fetch are independent reads against a remote
	// DB; run them concurrently to collapse two sequential round-trips into one.
	var (
		total   int64
		docs    []models.Document
		listErr error
		wg      sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		buildQuery().Count(&total)
	}()
	go func() {
		defer wg.Done()
		listErr = buildQuery().Preload("Tags").Order("documents.created_at DESC").Limit(limit).Offset(offset).Find(&docs).Error
	}()
	wg.Wait()
	if listErr != nil {
		response.InternalError(c, "LIST_FAILED", "Could not load documents.")
		return
	}

	response.OKWithMeta(c, "Documents retrieved", docs, &response.Meta{Page: page, Limit: limit, Total: total})
}

type createDocumentReq struct {
	Name           string         `json:"name"`
	FolderID       *string        `json:"folderId"`
	OrganizationID *string        `json:"organizationId"`
	FileType       string         `json:"fileType"`
	MimeType       string         `json:"mimeType"`
	FileSize       int64          `json:"fileSize"`
	StoragePath    string         `json:"storagePath"`
	Status         string         `json:"status"`
	Metadata       datatypes.JSON `json:"metadata"`
}

// CreateDocument registers a new document (metadata only; bytes live in storage).
// Creating in an organization requires an editor+ role in that org.
func CreateDocument(c *gin.Context) {
	uid, _ := userID(c)
	var req createDocumentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_BODY", "Invalid request body.")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		response.BadRequest(c, "INVALID_NAME", "Document name is required.")
		return
	}
	orgIDStr := ""
	if req.OrganizationID != nil {
		orgIDStr = *req.OrganizationID
	}
	orgID, ok := resolveOrg(c, orgIDStr, "editor")
	if !ok {
		return
	}
	fid, ok := parseUUID(req.FolderID)
	if !ok {
		response.BadRequest(c, "INVALID_FOLDER", "folderId must be a valid UUID.")
		return
	}
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = models.StatusUploaded
	}
	doc := models.Document{
		UserID:         uid,
		OrganizationID: orgID,
		FolderID:       fid,
		Name:           strings.TrimSpace(req.Name),
		FileType:       req.FileType,
		MimeType:       req.MimeType,
		FileSize:       req.FileSize,
		StoragePath:    req.StoragePath,
		Status:         status,
		Metadata:       req.Metadata,
	}
	if err := models.DB.Create(&doc).Error; err != nil {
		response.InternalError(c, "CREATE_FAILED", "Could not create document.")
		return
	}
	response.Created(c, "Document created", doc)
}

// GetDocument returns a single owned document.
func GetDocument(c *gin.Context) {
	uid, _ := userID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid document id.")
		return
	}
	orgID, ok := resolveOrg(c, c.Query("orgId"), "viewer")
	if !ok {
		return
	}
	var doc models.Document
	if err := scopeOwned(models.DB.Preload("Tags"), uid, orgID).Where("id = ?", id).First(&doc).Error; err != nil {
		response.NotFound(c, "NOT_FOUND", "Document not found.")
		return
	}
	response.OK(c, "Document retrieved", doc)
}

type updateDocumentReq struct {
	Name     *string        `json:"name"`
	FolderID *string        `json:"folderId"`
	Status   *string        `json:"status"`
	Metadata datatypes.JSON `json:"metadata"`
	// Move the document between scopes: an org id (editor+ in the target org
	// required), or "" to move it to the caller's personal space.
	OrganizationID *string `json:"organizationId"`
}

// UpdateDocument renames, moves, restatuses, re-tags, or re-scopes a document.
func UpdateDocument(c *gin.Context) {
	uid, _ := userID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid document id.")
		return
	}
	orgID, ok := resolveOrg(c, c.Query("orgId"), "editor")
	if !ok {
		return
	}
	var req updateDocumentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_BODY", "Invalid request body.")
		return
	}

	updates := map[string]any{}
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			response.BadRequest(c, "INVALID_NAME", "Document name cannot be empty.")
			return
		}
		updates["name"] = strings.TrimSpace(*req.Name)
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}
	if req.FolderID != nil {
		fid, ok := parseUUID(req.FolderID)
		if !ok {
			response.BadRequest(c, "INVALID_FOLDER", "folderId must be a valid UUID.")
			return
		}
		updates["folder_id"] = fid // empty string → nil → moves to root
	}
	if len(req.Metadata) > 0 {
		updates["metadata"] = req.Metadata
	}
	if req.OrganizationID != nil {
		// Moving scope: verify the caller can write in the target (editor+),
		// or "" for personal. nil target sets organization_id NULL.
		target, ok := resolveOrg(c, *req.OrganizationID, "editor")
		if !ok {
			return
		}
		updates["organization_id"] = target
	}
	if len(updates) == 0 {
		response.BadRequest(c, "NO_CHANGES", "No fields to update.")
		return
	}

	res := scopeOwned(models.DB.Model(&models.Document{}), uid, orgID).Where("id = ?", id).Updates(updates)
	if res.Error != nil {
		response.InternalError(c, "UPDATE_FAILED", "Could not update document.")
		return
	}
	if res.RowsAffected == 0 {
		response.NotFound(c, "NOT_FOUND", "Document not found.")
		return
	}

	var doc models.Document
	scopeOwned(models.DB.Preload("Tags"), uid, orgID).Where("id = ?", id).First(&doc)
	response.OK(c, "Document updated", doc)
}

// DeleteDocument soft-deletes a document (moves it to Trash).
func DeleteDocument(c *gin.Context) {
	uid, _ := userID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid document id.")
		return
	}
	orgID, ok := resolveOrg(c, c.Query("orgId"), "editor")
	if !ok {
		return
	}
	res := scopeOwned(models.DB, uid, orgID).Where("id = ?", id).Delete(&models.Document{})
	if res.Error != nil {
		response.InternalError(c, "DELETE_FAILED", "Could not delete document.")
		return
	}
	if res.RowsAffected == 0 {
		response.NotFound(c, "NOT_FOUND", "Document not found.")
		return
	}
	response.OK(c, "Document deleted", gin.H{"id": id})
}

// RestoreDocument brings a soft-deleted document back from Trash.
func RestoreDocument(c *gin.Context) {
	uid, _ := userID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid document id.")
		return
	}
	orgID, ok := resolveOrg(c, c.Query("orgId"), "editor")
	if !ok {
		return
	}
	res := scopeOwned(models.DB.Unscoped().Model(&models.Document{}), uid, orgID).
		Where("id = ? AND deleted_at IS NOT NULL", id).
		Update("deleted_at", nil)
	if res.Error != nil {
		response.InternalError(c, "RESTORE_FAILED", "Could not restore document.")
		return
	}
	if res.RowsAffected == 0 {
		response.NotFound(c, "NOT_FOUND", "Document not found in Trash.")
		return
	}
	response.OK(c, "Document restored", gin.H{"id": id})
}

// PurgeDocument permanently deletes a document (hard delete).
func PurgeDocument(c *gin.Context) {
	uid, _ := userID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid document id.")
		return
	}
	orgID, ok := resolveOrg(c, c.Query("orgId"), "editor")
	if !ok {
		return
	}
	res := scopeOwned(models.DB.Unscoped(), uid, orgID).Where("id = ?", id).Delete(&models.Document{})
	if res.Error != nil {
		response.InternalError(c, "PURGE_FAILED", "Could not permanently delete document.")
		return
	}
	if res.RowsAffected == 0 {
		response.NotFound(c, "NOT_FOUND", "Document not found.")
		return
	}
	models.DB.Exec("DELETE FROM document_tags WHERE document_id = ?", id)
	response.OK(c, "Document permanently deleted", gin.H{"id": id})
}

type attachTagReq struct {
	TagID string `json:"tagId"`
}

// AttachTag links an owned tag to an owned document.
func AttachTag(c *gin.Context) {
	uid, _ := userID(c)
	docID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid document id.")
		return
	}
	var req attachTagReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_BODY", "Invalid request body.")
		return
	}
	tagID, err := uuid.Parse(strings.TrimSpace(req.TagID))
	if err != nil {
		response.BadRequest(c, "INVALID_TAG", "tagId must be a valid UUID.")
		return
	}
	orgID, ok := resolveOrg(c, c.Query("orgId"), "editor")
	if !ok {
		return
	}

	var doc models.Document
	if err := scopeOwned(models.DB, uid, orgID).Where("id = ?", docID).First(&doc).Error; err != nil {
		response.NotFound(c, "NOT_FOUND", "Document not found.")
		return
	}
	var tag models.Tag
	if err := scopeOwned(models.DB, uid, orgID).Where("id = ?", tagID).First(&tag).Error; err != nil {
		response.NotFound(c, "TAG_NOT_FOUND", "Tag not found.")
		return
	}
	if err := models.DB.Model(&doc).Association("Tags").Append(&tag); err != nil {
		response.InternalError(c, "ATTACH_FAILED", "Could not attach tag.")
		return
	}
	response.OK(c, "Tag attached", gin.H{"documentId": docID, "tagId": tagID})
}

// DetachTag removes a tag from a document.
func DetachTag(c *gin.Context) {
	uid, _ := userID(c)
	docID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid document id.")
		return
	}
	tagID, err := uuid.Parse(c.Param("tagId"))
	if err != nil {
		response.BadRequest(c, "INVALID_TAG", "Invalid tag id.")
		return
	}
	orgID, ok := resolveOrg(c, c.Query("orgId"), "editor")
	if !ok {
		return
	}
	var doc models.Document
	if err := scopeOwned(models.DB, uid, orgID).Where("id = ?", docID).First(&doc).Error; err != nil {
		response.NotFound(c, "NOT_FOUND", "Document not found.")
		return
	}
	if err := models.DB.Model(&doc).Association("Tags").Delete(&models.Tag{ID: tagID}); err != nil {
		response.InternalError(c, "DETACH_FAILED", "Could not detach tag.")
		return
	}
	response.OK(c, "Tag detached", gin.H{"documentId": docID, "tagId": tagID})
}

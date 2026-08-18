// Package handlers implements document-service's HTTP surface: the document
// library, folders, tags, saved exports, and job workspace hints.
//
// Two constraints shape this package. Identity arrives as a gateway-injected
// header (see RequireUser in auth.go) and organization permissions are resolved
// by calling user-service rather than reading its tables. And exports are
// bounded by a process-wide concurrency semaphore, because each one scans a
// user's whole library and builds the archive in memory — see exports.go.
package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"document-service/internal/models"
	"document-service/internal/orgclient"
	"fyredocs/shared/logger"
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
		response.Errorf(c, http.StatusBadGateway, "ORG_CHECK_FAILED", "Could not verify organization access.", err,
			"op", "orgclient.membership", "orgId", orgID.String(), "userId", uid.String())
		return nil, false
	}
	if !member {
		response.NotFound(c, response.CodeNotFound, "Organization not found.")
		return nil, false
	}
	if !roleAtLeast(role, minRole) {
		response.Forbidden(c, response.CodeForbidden, "You don't have permission for this action.")
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

// maxListPage bounds OFFSET depth on the document list (see the note at the
// clamp below).
const maxListPage = 200

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
	// Deep OFFSET degrades linearly — the database still walks every skipped
	// row — so page depth is capped instead of being effectively unbounded.
	// Reaching this cap means the caller wants a different query (filters or a
	// narrower window), not page 4000 of an append-only table.
	page := queryInt(c, "page", 1)
	if page > maxListPage {
		page = maxListPage
	}
	if page < 1 {
		page = 1
	}
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
	// Delta-sync support (Phase 9): only rows touched after this instant.
	// Invalid values are rejected rather than ignored — a sync client that
	// silently full-lists would re-download everything.
	var updatedSince *time.Time
	if raw := strings.TrimSpace(c.Query("updatedSince")); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			response.BadRequest(c, response.CodeInvalidInput, "updatedSince must be an RFC3339 timestamp.")
			return
		}
		updatedSince = &parsed
	}
	sortUpdated := c.Query("sort") == "updated"

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
		if updatedSince != nil {
			q = q.Where("documents.updated_at > ?", *updatedSince)
		}
		return q
	}

	// The total count and the page fetch are independent reads against a remote
	// DB; run them concurrently to collapse two sequential round-trips into one.
	var (
		total    int64
		docs     []models.Document
		listErr  error
		countErr error
		wg       sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		countErr = buildQuery().Count(&total).Error
	}()
	go func() {
		defer wg.Done()
		order := "documents.created_at DESC"
		if sortUpdated {
			// Delta-sync clients page in modification order (Phase 9).
			order = "documents.updated_at DESC"
		}
		listErr = buildQuery().Preload("Tags").Order(order).Limit(limit).Offset(offset).Find(&docs).Error
	}()
	wg.Wait()
	if listErr != nil {
		response.InternalErrorf(c, response.CodeServerError, "Could not load documents.", listErr,
			"op", "db.documents.list", "userId", uid)
		return
	}
	if countErr != nil {
		logger.LogWarn(c.Request.Context(), "db.documents.count", countErr, "userId", uid)
	}

	response.OKWithMeta(c, "Documents retrieved", docs, &response.Meta{Page: page, Limit: limit, Total: total})
}

// createDocumentReq is the create-document body. Ownership is never taken from
// the body — it comes from the authenticated caller — so a client cannot file a
// document into someone else's library.
type createDocumentReq struct {
	Name           string         `json:"name" binding:"required,max=255"`
	FolderID       *string        `json:"folderId" binding:"omitempty,uuid"`
	OrganizationID *string        `json:"organizationId" binding:"omitempty,uuid"`
	FileType       string         `json:"fileType" binding:"omitempty,max=64"`
	MimeType       string         `json:"mimeType" binding:"omitempty,max=128"`
	FileSize       int64          `json:"fileSize" binding:"omitempty,gte=0"`
	StoragePath    string         `json:"storagePath" binding:"omitempty,max=1024"`
	Status         string         `json:"status" binding:"omitempty,oneof=uploaded processing ready failed"`
	Metadata       datatypes.JSON `json:"metadata"`
}

// CreateDocument registers a new document (metadata only; bytes live in storage).
// Creating in an organization requires an editor+ role in that org.
func CreateDocument(c *gin.Context) {
	uid, _ := userID(c)
	var req createDocumentReq
	if !response.BindJSON(c, &req) {
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
	if !storagePathAllowed(req.StoragePath, uid) {
		response.BadRequest(c, response.CodeInvalidInput, "storagePath does not belong to this account.")
		return
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
		response.InternalErrorf(c, response.CodeServerError, "Could not create document.", err,
			"op", "db.documents.create", "userId", uid)
		return
	}
	response.Created(c, "Document created", doc)
}

// GetDocument returns a single owned document.
func GetDocument(c *gin.Context) {
	uid, _ := userID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, response.CodeInvalidID, "Invalid document id.")
		return
	}
	orgID, ok := resolveOrg(c, c.Query("orgId"), "viewer")
	if !ok {
		return
	}
	var doc models.Document
	if err := scopeOwned(models.DB.Preload("Tags"), uid, orgID).Where("id = ?", id).First(&doc).Error; err != nil {
		response.NotFound(c, response.CodeNotFound, "Document not found.")
		return
	}
	response.OK(c, "Document retrieved", doc)
}

// storagePathAllowed enforces storagePath hygiene (Phase 9): the backups/
// prefix is reserved for job-service's backup endpoint, whose keys embed the
// OWNER's user id — a caller may only register keys under their own prefix.
// Every other shape (job outputs, empty) passes through unvalidated, exactly
// as before, for web-app compatibility.
func storagePathAllowed(path string, uid uuid.UUID) bool {
	if !strings.HasPrefix(path, "backups/") {
		return true
	}
	if !strings.HasPrefix(path, "backups/"+uid.String()+"/") {
		return false
	}
	// A ".." segment can re-resolve the ownership prefix at any hop that
	// normalizes dot segments (edge proxy, presign URL handling), turning
	// backups/<me>/../<other>/f into <other>'s key. Legitimate backup keys
	// are server-minted (uuid + sanitized file name — see job-service
	// backups.go), so a bare ".." segment never appears in one and rejecting
	// it breaks nothing.
	for _, seg := range strings.Split(path, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}

// downloadURLExpiry matches job-service's presign window: long enough to
// follow a redirect, short enough that a leaked URL is useless.
const downloadURLExpiry = 5 * time.Minute

// DownloadDocument 302s to a presigned GET for the document's stored bytes
// (Phase 9). Only rows with a StoragePath have bytes here — job-era rows
// (bytes behind the job download route, possibly expired) 404 and the client
// falls back to the job route.
func DownloadDocument(c *gin.Context) {
	uid, _ := userID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, response.CodeInvalidID, "Invalid document id.")
		return
	}
	orgID, ok := resolveOrg(c, c.Query("orgId"), "viewer")
	if !ok {
		return
	}
	var doc models.Document
	if err := scopeOwned(models.DB, uid, orgID).Where("id = ?", id).First(&doc).Error; err != nil {
		response.NotFound(c, response.CodeNotFound, "Document not found.")
		return
	}
	if strings.TrimSpace(doc.StoragePath) == "" {
		response.NotFound(c, response.CodeNotFound, "This document has no stored file.")
		return
	}
	if objStore == nil {
		response.InternalError(c, response.CodeServerError, "Downloads are unavailable. Please try again later.")
		return
	}

	fileName := doc.Name
	if doc.FileType != "" && !strings.HasSuffix(strings.ToLower(fileName), "."+strings.ToLower(doc.FileType)) {
		fileName += "." + doc.FileType
	}
	params := url.Values{}
	params.Set("response-content-disposition", fmt.Sprintf(`attachment; filename="%s"`, fileName))
	if doc.MimeType != "" {
		params.Set("response-content-type", doc.MimeType)
	}
	presigned, err := objStore.PresignGet(c.Request.Context(), objStore.BucketOutputs(), doc.StoragePath,
		downloadURLExpiry, params)
	if err != nil {
		response.InternalErrorf(c, response.CodeServerError, "Could not prepare the download. Please try again.", err,
			"op", "documents.presign", "docId", id)
		return
	}
	c.Redirect(302, presigned)
}

// updateDocumentReq is the patch body. Pointer fields distinguish "omitted" from
// "explicitly cleared", which a partial update needs in order to leave untouched
// fields alone.
type updateDocumentReq struct {
	Name     *string        `json:"name" binding:"omitempty,max=255"`
	FolderID *string        `json:"folderId" binding:"omitempty,uuid"`
	Status   *string        `json:"status" binding:"omitempty,oneof=uploaded processing ready failed"`
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
		response.BadRequest(c, response.CodeInvalidID, "Invalid document id.")
		return
	}
	orgID, ok := resolveOrg(c, c.Query("orgId"), "editor")
	if !ok {
		return
	}
	var req updateDocumentReq
	if !response.BindJSON(c, &req) {
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
		response.InternalErrorf(c, response.CodeServerError, "Could not update document.", res.Error,
			"op", "db.documents.update", "docId", id)
		return
	}
	if res.RowsAffected == 0 {
		response.NotFound(c, response.CodeNotFound, "Document not found.")
		return
	}

	var doc models.Document
	if err := scopeOwned(models.DB.Preload("Tags"), uid, orgID).Where("id = ?", id).First(&doc).Error; err != nil {
		logger.LogWarn(c.Request.Context(), "db.documents.reload", err, "docId", id)
	}
	response.OK(c, "Document updated", doc)
}

// DeleteDocument soft-deletes a document (moves it to Trash).
func DeleteDocument(c *gin.Context) {
	uid, _ := userID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, response.CodeInvalidID, "Invalid document id.")
		return
	}
	orgID, ok := resolveOrg(c, c.Query("orgId"), "editor")
	if !ok {
		return
	}
	res := scopeOwned(models.DB, uid, orgID).Where("id = ?", id).Delete(&models.Document{})
	if res.Error != nil {
		response.InternalErrorf(c, "DELETE_FAILED", "Could not delete document.", res.Error,
			"op", "db.documents.soft_delete", "docId", id)
		return
	}
	if res.RowsAffected == 0 {
		response.NotFound(c, response.CodeNotFound, "Document not found.")
		return
	}
	response.OK(c, "Document deleted", gin.H{"id": id})
}

// RestoreDocument brings a soft-deleted document back from Trash.
func RestoreDocument(c *gin.Context) {
	uid, _ := userID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, response.CodeInvalidID, "Invalid document id.")
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
		response.InternalErrorf(c, "RESTORE_FAILED", "Could not restore document.", res.Error,
			"op", "db.documents.restore", "docId", id)
		return
	}
	if res.RowsAffected == 0 {
		response.NotFound(c, response.CodeNotFound, "Document not found in Trash.")
		return
	}
	response.OK(c, "Document restored", gin.H{"id": id})
}

// PurgeDocument permanently deletes a document (hard delete).
func PurgeDocument(c *gin.Context) {
	uid, _ := userID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, response.CodeInvalidID, "Invalid document id.")
		return
	}
	orgID, ok := resolveOrg(c, c.Query("orgId"), "editor")
	if !ok {
		return
	}
	// Fetch first: purge must also delete backup BYTES (backups/ objects are
	// swept by nothing else — job-service's cleanups deliberately skip them).
	var doc models.Document
	if err := scopeOwned(models.DB.Unscoped(), uid, orgID).Where("id = ?", id).First(&doc).Error; err != nil {
		response.NotFound(c, response.CodeNotFound, "Document not found.")
		return
	}
	// Associations must go BEFORE the row: fk_document_tags_document rejects
	// deleting a document that still has tag rows, which made every tagged
	// document permanently unpurgeable (500). Ownership is already proven by
	// the scoped fetch above, so this cannot strip tags off a foreign row.
	if err := models.DB.Exec("DELETE FROM document_tags WHERE document_id = ?", id).Error; err != nil {
		response.InternalErrorf(c, "PURGE_FAILED", "Could not permanently delete document.", err,
			"op", "db.document_tags.purge", "docId", id)
		return
	}
	res := scopeOwned(models.DB.Unscoped(), uid, orgID).Where("id = ?", id).Delete(&models.Document{})
	if res.Error != nil {
		response.InternalErrorf(c, "PURGE_FAILED", "Could not permanently delete document.", res.Error,
			"op", "db.documents.purge", "docId", id)
		return
	}
	if res.RowsAffected == 0 {
		response.NotFound(c, response.CodeNotFound, "Document not found.")
		return
	}
	if strings.HasPrefix(doc.StoragePath, "backups/") {
		if objStore == nil {
			logger.LogWarn(c.Request.Context(), "documents.purge.storage_unavailable", nil, "docId", id, "key", doc.StoragePath)
		} else if err := objStore.RemoveObject(c.Request.Context(), objStore.BucketOutputs(), doc.StoragePath); err != nil {
			// Row is gone (truth); a leaked object is logged, not fatal.
			logger.LogWarn(c.Request.Context(), "documents.purge.remove_object", err, "docId", id, "key", doc.StoragePath)
		}
	}
	response.OK(c, "Document permanently deleted", gin.H{"id": id})
}

// attachTagReq names the tag to attach. The tag must belong to the same owner as
// the document, checked by the handler.
type attachTagReq struct {
	TagID string `json:"tagId" binding:"required,uuid"`
}

// AttachTag links an owned tag to an owned document.
func AttachTag(c *gin.Context) {
	uid, _ := userID(c)
	docID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, response.CodeInvalidID, "Invalid document id.")
		return
	}
	var req attachTagReq
	if !response.BindJSON(c, &req) {
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
		response.NotFound(c, response.CodeNotFound, "Document not found.")
		return
	}
	var tag models.Tag
	if err := scopeOwned(models.DB, uid, orgID).Where("id = ?", tagID).First(&tag).Error; err != nil {
		response.NotFound(c, "TAG_NOT_FOUND", "Tag not found.")
		return
	}
	if err := models.DB.Model(&doc).Association("Tags").Append(&tag); err != nil {
		response.InternalErrorf(c, "ATTACH_FAILED", "Could not attach tag.", err,
			"op", "db.document_tags.attach", "docId", docID, "tagId", tagID)
		return
	}
	response.OK(c, "Tag attached", gin.H{"documentId": docID, "tagId": tagID})
}

// DetachTag removes a tag from a document.
func DetachTag(c *gin.Context) {
	uid, _ := userID(c)
	docID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, response.CodeInvalidID, "Invalid document id.")
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
		response.NotFound(c, response.CodeNotFound, "Document not found.")
		return
	}
	if err := models.DB.Model(&doc).Association("Tags").Delete(&models.Tag{ID: tagID}); err != nil {
		response.InternalErrorf(c, "DETACH_FAILED", "Could not detach tag.", err,
			"op", "db.document_tags.detach", "docId", docID, "tagId", tagID)
		return
	}
	response.OK(c, "Tag detached", gin.H{"documentId": docID, "tagId": tagID})
}

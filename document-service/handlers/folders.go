package handlers

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"document-service/internal/models"
	"fyredocs/shared/response"
)

// ListFolders returns folders in scope (personal or org), optionally by parent.
func ListFolders(c *gin.Context) {
	uid, _ := userID(c)
	orgID, ok := resolveOrg(c, c.Query("orgId"), "viewer")
	if !ok {
		return
	}
	q := scopeOwned(models.DB, uid, orgID)
	if parent := strings.TrimSpace(c.Query("parentId")); parent != "" {
		if pid, err := uuid.Parse(parent); err == nil {
			q = q.Where("parent_id = ?", pid)
		}
	}
	var folders []models.Folder
	if err := q.Order("name ASC").Find(&folders).Error; err != nil {
		response.InternalError(c, "LIST_FAILED", "Could not load folders.")
		return
	}
	response.OK(c, "Folders retrieved", folders)
}

type folderReq struct {
	Name           string  `json:"name"`
	ParentID       *string `json:"parentId"`
	OrganizationID *string `json:"organizationId"`
}

// CreateFolder creates a folder in scope (org requires editor+).
func CreateFolder(c *gin.Context) {
	uid, _ := userID(c)
	var req folderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_BODY", "Invalid request body.")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		response.BadRequest(c, "INVALID_NAME", "Folder name is required.")
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
	pid, ok := parseUUID(req.ParentID)
	if !ok {
		response.BadRequest(c, "INVALID_PARENT", "parentId must be a valid UUID.")
		return
	}
	folder := models.Folder{UserID: uid, OrganizationID: orgID, Name: strings.TrimSpace(req.Name), ParentID: pid}
	if err := models.DB.Create(&folder).Error; err != nil {
		response.InternalError(c, "CREATE_FAILED", "Could not create folder.")
		return
	}
	response.Created(c, "Folder created", folder)
}

// UpdateFolder renames or moves a folder (org requires editor+).
func UpdateFolder(c *gin.Context) {
	uid, _ := userID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid folder id.")
		return
	}
	orgID, ok := resolveOrg(c, c.Query("orgId"), "editor")
	if !ok {
		return
	}
	var req folderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_BODY", "Invalid request body.")
		return
	}
	updates := map[string]any{}
	if strings.TrimSpace(req.Name) != "" {
		updates["name"] = strings.TrimSpace(req.Name)
	}
	if req.ParentID != nil {
		pid, ok := parseUUID(req.ParentID)
		if !ok {
			response.BadRequest(c, "INVALID_PARENT", "parentId must be a valid UUID.")
			return
		}
		if pid != nil && *pid == id {
			response.BadRequest(c, "INVALID_PARENT", "A folder cannot be its own parent.")
			return
		}
		updates["parent_id"] = pid
	}
	if len(updates) == 0 {
		response.BadRequest(c, "NO_CHANGES", "No fields to update.")
		return
	}
	res := scopeOwned(models.DB.Model(&models.Folder{}), uid, orgID).Where("id = ?", id).Updates(updates)
	if res.Error != nil {
		response.InternalError(c, "UPDATE_FAILED", "Could not update folder.")
		return
	}
	if res.RowsAffected == 0 {
		response.NotFound(c, "NOT_FOUND", "Folder not found.")
		return
	}
	response.OK(c, "Folder updated", gin.H{"id": id})
}

// DeleteFolder soft-deletes a folder; its documents move to root (org requires editor+).
func DeleteFolder(c *gin.Context) {
	uid, _ := userID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid folder id.")
		return
	}
	orgID, ok := resolveOrg(c, c.Query("orgId"), "editor")
	if !ok {
		return
	}
	res := scopeOwned(models.DB, uid, orgID).Where("id = ?", id).Delete(&models.Folder{})
	if res.Error != nil {
		response.InternalError(c, "DELETE_FAILED", "Could not delete folder.")
		return
	}
	if res.RowsAffected == 0 {
		response.NotFound(c, "NOT_FOUND", "Folder not found.")
		return
	}
	scopeOwned(models.DB.Model(&models.Document{}), uid, orgID).Where("folder_id = ?", id).Update("folder_id", nil)
	response.OK(c, "Folder deleted", gin.H{"id": id})
}

package handlers

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"document-service/internal/models"
	"fyredocs/shared/logger"
	"fyredocs/shared/response"
)

// ListTags returns tags in scope (personal or org).
func ListTags(c *gin.Context) {
	uid, _ := userID(c)
	orgID, ok := resolveOrg(c, c.Query("orgId"), "viewer")
	if !ok {
		return
	}
	var tags []models.Tag
	if err := scopeOwned(models.DB, uid, orgID).Order("name ASC").Find(&tags).Error; err != nil {
		response.InternalErrorf(c, "LIST_FAILED", "Could not load tags.", err, "op", "db.tags.list", "userId", uid)
		return
	}
	response.OK(c, "Tags retrieved", tags)
}

type tagReq struct {
	Name           string  `json:"name"`
	Color          string  `json:"color"`
	OrganizationID *string `json:"organizationId"`
}

// CreateTag creates a tag in scope (idempotent within the scope; org requires editor+).
func CreateTag(c *gin.Context) {
	uid, _ := userID(c)
	var req tagReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_BODY", "Invalid request body.")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		response.BadRequest(c, "INVALID_NAME", "Tag name is required.")
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
	var existing models.Tag
	if err := scopeOwned(models.DB, uid, orgID).Where("name = ?", name).First(&existing).Error; err == nil {
		response.OK(c, "Tag already exists", existing)
		return
	}
	tag := models.Tag{UserID: uid, OrganizationID: orgID, Name: name, Color: strings.TrimSpace(req.Color)}
	if err := models.DB.Create(&tag).Error; err != nil {
		response.InternalErrorf(c, "CREATE_FAILED", "Could not create tag.", err, "op", "db.tags.create", "userId", uid)
		return
	}
	response.Created(c, "Tag created", tag)
}

// DeleteTag removes a tag in scope (and its document associations).
func DeleteTag(c *gin.Context) {
	uid, _ := userID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid tag id.")
		return
	}
	orgID, ok := resolveOrg(c, c.Query("orgId"), "editor")
	if !ok {
		return
	}
	res := scopeOwned(models.DB, uid, orgID).Where("id = ?", id).Delete(&models.Tag{})
	if res.Error != nil {
		response.InternalErrorf(c, "DELETE_FAILED", "Could not delete tag.", res.Error, "op", "db.tags.delete", "tagId", id)
		return
	}
	if res.RowsAffected == 0 {
		response.NotFound(c, "NOT_FOUND", "Tag not found.")
		return
	}
	if err := models.DB.Exec("DELETE FROM document_tags WHERE tag_id = ?", id).Error; err != nil {
		logger.LogWarn(c.Request.Context(), "db.document_tags.cleanup", err, "tagId", id)
	}
	response.OK(c, "Tag deleted", gin.H{"id": id})
}

package handlers

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"fyredocs/shared/logger"
	"fyredocs/shared/response"

	"user-service/internal/models"
)

// pageParams reads page/limit query params with a sane default (25) and hard
// cap (100), returning the resolved page, limit, and SQL offset. Prevents
// unbounded list responses (a large org/membership set as one payload).
func pageParams(c *gin.Context) (page, limit, offset int) {
	page = 1
	if v, err := strconv.Atoi(c.Query("page")); err == nil && v > 0 {
		page = v
	}
	limit = 25
	if v, err := strconv.Atoi(c.Query("limit")); err == nil && v > 0 {
		limit = v
	}
	if limit > 100 {
		limit = 100
	}
	return page, limit, (page - 1) * limit
}

// ListOrganizations returns the organizations the caller belongs to, each with
// the caller's role.
func ListOrganizations(c *gin.Context) {
	uid, _ := userID(c)
	page, limit, offset := pageParams(c)

	var total int64
	if err := models.DB.Model(&models.Membership{}).Where("user_id = ?", uid).Count(&total).Error; err != nil {
		response.InternalErrorf(c, "LIST_FAILED", "Could not load organizations.", err,
			"op", "db.memberships.count", "userId", uid)
		return
	}
	if total == 0 {
		response.OKWithMeta(c, "Organizations retrieved", []any{}, &response.Meta{Page: page, Limit: limit, Total: 0})
		return
	}

	var memberships []models.Membership
	if err := models.DB.Where("user_id = ?", uid).Order("created_at ASC").
		Limit(limit).Offset(offset).Find(&memberships).Error; err != nil {
		response.InternalErrorf(c, "LIST_FAILED", "Could not load organizations.", err,
			"op", "db.memberships.list", "userId", uid)
		return
	}
	roleByOrg := make(map[uuid.UUID]string, len(memberships))
	ids := make([]uuid.UUID, 0, len(memberships))
	for _, m := range memberships {
		roleByOrg[m.OrganizationID] = m.Role
		ids = append(ids, m.OrganizationID)
	}
	var orgs []models.Organization
	if len(ids) > 0 {
		if err := models.DB.Where("id IN ?", ids).Order("created_at ASC").Find(&orgs).Error; err != nil {
			logger.LogWarn(c.Request.Context(), "db.orgs.list_by_ids", err, "userId", uid)
		}
	}

	out := make([]gin.H, 0, len(orgs))
	for _, o := range orgs {
		out = append(out, gin.H{
			"id": o.ID, "name": o.Name, "slug": o.Slug, "ownerUserId": o.OwnerUserID,
			"planName": o.PlanName, "createdAt": o.CreatedAt, "role": roleByOrg[o.ID],
		})
	}
	response.OKWithMeta(c, "Organizations retrieved", out, &response.Meta{Page: page, Limit: limit, Total: total})
}

type createOrgReq struct {
	Name string `json:"name"`
}

// CreateOrganization creates an org and an owner membership for the caller.
func CreateOrganization(c *gin.Context) {
	uid, _ := userID(c)
	var req createOrgReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_BODY", "Invalid request body.")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		response.BadRequest(c, "INVALID_NAME", "Organization name is required.")
		return
	}
	// Suffix from the random tail of a UUID — NOT the prefix, which on a v7 UUID
	// is a near-constant timestamp and collides for same-named orgs.
	sfx := uuid.Must(uuid.NewV7()).String()
	org := models.Organization{
		Name:        name,
		Slug:        slugify(name) + "-" + sfx[len(sfx)-8:],
		OwnerUserID: uid,
		PlanName:    "free",
	}
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&org).Error; err != nil {
			return err
		}
		return tx.Create(&models.Membership{OrganizationID: org.ID, UserID: uid, Role: models.RoleOwner}).Error
	})
	if err != nil {
		response.InternalErrorf(c, "CREATE_FAILED", "Could not create organization.", err,
			"op", "db.orgs.create_tx", "userId", uid)
		return
	}
	response.Created(c, "Organization created", gin.H{
		"id": org.ID, "name": org.Name, "slug": org.Slug, "ownerUserId": org.OwnerUserID,
		"planName": org.PlanName, "createdAt": org.CreatedAt, "role": models.RoleOwner,
	})
}

// orgParam parses :id and verifies the caller is a member; writes the error
// response and returns ok=false otherwise.
func orgParam(c *gin.Context) (uuid.UUID, string, bool) {
	uid, _ := userID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid organization id.")
		return uuid.Nil, "", false
	}
	role, ok := membershipRole(c.Request.Context(), id, uid)
	if !ok {
		response.NotFound(c, "NOT_FOUND", "Organization not found.")
		return uuid.Nil, "", false
	}
	return id, role, true
}

// GetOrganization returns an org the caller belongs to.
func GetOrganization(c *gin.Context) {
	orgID, role, ok := orgParam(c)
	if !ok {
		return
	}
	var org models.Organization
	if err := models.DB.First(&org, "id = ?", orgID).Error; err != nil {
		response.NotFound(c, "NOT_FOUND", "Organization not found.")
		return
	}
	response.OK(c, "Organization retrieved", gin.H{
		"id": org.ID, "name": org.Name, "slug": org.Slug, "ownerUserId": org.OwnerUserID,
		"planName": org.PlanName, "createdAt": org.CreatedAt, "role": role,
	})
}

// ListMembers lists an org's members (any member may view).
func ListMembers(c *gin.Context) {
	orgID, _, ok := orgParam(c)
	if !ok {
		return
	}
	page, limit, offset := pageParams(c)
	var total int64
	if err := models.DB.Model(&models.Membership{}).Where("organization_id = ?", orgID).Count(&total).Error; err != nil {
		logger.LogWarn(c.Request.Context(), "db.memberships.count", err, "orgId", orgID)
	}
	var members []models.Membership
	if err := models.DB.Where("organization_id = ?", orgID).Order("created_at ASC").
		Limit(limit).Offset(offset).Find(&members).Error; err != nil {
		logger.LogWarn(c.Request.Context(), "db.memberships.list", err, "orgId", orgID)
	}
	response.OKWithMeta(c, "Members retrieved", members, &response.Meta{Page: page, Limit: limit, Total: total})
}

type addMemberReq struct {
	UserID string `json:"userId"`
	Role   string `json:"role"`
}

// AddMember adds or updates a member's role (admin/owner only).
func AddMember(c *gin.Context) {
	orgID, role, ok := orgParam(c)
	if !ok {
		return
	}
	if !models.RoleAtLeast(role, models.RoleAdmin) {
		response.Forbidden(c, "FORBIDDEN", "You need an admin role to manage members.")
		return
	}
	var req addMemberReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_BODY", "Invalid request body.")
		return
	}
	targetID, err := uuid.Parse(strings.TrimSpace(req.UserID))
	if err != nil {
		response.BadRequest(c, "INVALID_USER", "userId must be a valid UUID.")
		return
	}
	newRole := strings.TrimSpace(req.Role)
	if newRole == "" {
		newRole = models.RoleViewer
	}
	// Owner can only be set via ownership transfer (out of scope here).
	if newRole == models.RoleOwner || !models.ValidRole(newRole) {
		response.BadRequest(c, "INVALID_ROLE", "Role must be admin, editor, or viewer.")
		return
	}

	var existing models.Membership
	tx := models.DB.Where("organization_id = ? AND user_id = ?", orgID, targetID).First(&existing)
	if tx.Error == nil {
		// Don't change the owner's membership through this endpoint.
		if existing.Role == models.RoleOwner {
			response.Forbidden(c, "FORBIDDEN", "The organization owner's role cannot be changed here.")
			return
		}
		if err := models.DB.Model(&existing).Update("role", newRole).Error; err != nil {
			response.InternalErrorf(c, "UPDATE_FAILED", "Could not update member.", err,
				"op", "db.memberships.update_role", "orgId", orgID, "targetUserId", targetID)
			return
		}
		response.OK(c, "Member updated", existing)
		return
	}
	m := models.Membership{OrganizationID: orgID, UserID: targetID, Role: newRole}
	// Upsert on (org, user): a concurrent double-add updates the role instead of
	// racing to a unique-violation 500 (idempotent add).
	if err := models.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "organization_id"}, {Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"role"}),
	}).Create(&m).Error; err != nil {
		response.InternalErrorf(c, "ADD_FAILED", "Could not add member.", err,
			"op", "db.memberships.create", "orgId", orgID, "targetUserId", targetID)
		return
	}
	response.Created(c, "Member added", m)
}

type updateMemberReq struct {
	Role string `json:"role"`
}

// UpdateMemberRole changes a member's role (admin/owner only; not the owner).
func UpdateMemberRole(c *gin.Context) {
	orgID, role, ok := orgParam(c)
	if !ok {
		return
	}
	if !models.RoleAtLeast(role, models.RoleAdmin) {
		response.Forbidden(c, "FORBIDDEN", "You need an admin role to manage members.")
		return
	}
	targetID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		response.BadRequest(c, "INVALID_USER", "Invalid user id.")
		return
	}
	var req updateMemberReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_BODY", "Invalid request body.")
		return
	}
	newRole := strings.TrimSpace(req.Role)
	if newRole == models.RoleOwner || !models.ValidRole(newRole) {
		response.BadRequest(c, "INVALID_ROLE", "Role must be admin, editor, or viewer.")
		return
	}
	var m models.Membership
	if err := models.DB.Where("organization_id = ? AND user_id = ?", orgID, targetID).First(&m).Error; err != nil {
		response.NotFound(c, "NOT_FOUND", "Member not found.")
		return
	}
	if m.Role == models.RoleOwner {
		response.Forbidden(c, "FORBIDDEN", "The organization owner's role cannot be changed.")
		return
	}
	if err := models.DB.Model(&m).Update("role", newRole).Error; err != nil {
		response.InternalErrorf(c, "UPDATE_FAILED", "Could not update member role.", err,
			"op", "db.memberships.update_role", "orgId", orgID, "targetUserId", targetID)
		return
	}
	response.OK(c, "Member role updated", m)
}

// RemoveMember removes a member (admin/owner only; never the owner).
func RemoveMember(c *gin.Context) {
	orgID, role, ok := orgParam(c)
	if !ok {
		return
	}
	if !models.RoleAtLeast(role, models.RoleAdmin) {
		response.Forbidden(c, "FORBIDDEN", "You need an admin role to manage members.")
		return
	}
	targetID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		response.BadRequest(c, "INVALID_USER", "Invalid user id.")
		return
	}
	var m models.Membership
	if err := models.DB.Where("organization_id = ? AND user_id = ?", orgID, targetID).First(&m).Error; err != nil {
		response.NotFound(c, "NOT_FOUND", "Member not found.")
		return
	}
	if m.Role == models.RoleOwner {
		response.Forbidden(c, "FORBIDDEN", "The organization owner cannot be removed.")
		return
	}
	if err := models.DB.Delete(&m).Error; err != nil {
		response.InternalErrorf(c, "REMOVE_FAILED", "Could not remove member.", err,
			"op", "db.memberships.delete", "orgId", orgID, "targetUserId", targetID)
		return
	}
	response.OK(c, "Member removed", gin.H{"organizationId": orgID, "userId": targetID})
}

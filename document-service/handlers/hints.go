package handlers

import (
	"context"
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"document-service/internal/models"
	"fyredocs/shared/logger"
	"fyredocs/shared/response"
)

type workspaceHintReq struct {
	JobID          string `json:"jobId"`
	OrganizationID string `json:"organizationId"`
}

// SetJobWorkspaceHint records that a job should finalize into an organization
// (editor+ required). An empty organizationId clears any hint (personal).
func SetJobWorkspaceHint(c *gin.Context) {
	uid, _ := userID(c)
	var req workspaceHintReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_BODY", "Invalid request body.")
		return
	}
	jobID, err := uuid.Parse(strings.TrimSpace(req.JobID))
	if err != nil {
		response.BadRequest(c, "INVALID_JOB", "jobId must be a valid UUID.")
		return
	}

	org := strings.TrimSpace(req.OrganizationID)
	if org == "" {
		if err := models.DB.Where("job_id = ? AND user_id = ?", jobID, uid).Delete(&models.JobWorkspaceHint{}).Error; err != nil {
			logger.LogWarn(c.Request.Context(), "db.job_hint.clear", err, "jobId", jobID)
		}
		response.OK(c, "Workspace hint cleared", gin.H{"jobId": jobID})
		return
	}
	orgID, ok := resolveOrg(c, org, "editor")
	if !ok {
		return
	}
	hint := models.JobWorkspaceHint{JobID: jobID, UserID: uid, OrganizationID: *orgID}
	if err := models.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "job_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"user_id", "organization_id"}),
	}).Create(&hint).Error; err != nil {
		response.InternalErrorf(c, "HINT_FAILED", "Could not set workspace.", err,
			"op", "db.job_hint.upsert", "jobId", jobID)
		return
	}
	response.OK(c, "Workspace hint set", hint)
}

// WorkspaceForJob returns the org a completed job should finalize into, or nil
// for personal. Read-only; call ClearJobWorkspace after a successful finalize.
func WorkspaceForJob(ctx context.Context, db *gorm.DB, jobID, userID uuid.UUID) *uuid.UUID {
	var hint models.JobWorkspaceHint
	if err := db.Where("job_id = ? AND user_id = ?", jobID, userID).First(&hint).Error; err != nil {
		// Not-found is the common case (personal scope); a real DB error should
		// not silently masquerade as "personal".
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			logger.LogWarn(ctx, "db.job_hint.lookup", err, "jobId", jobID, "userId", userID)
		}
		return nil
	}
	org := hint.OrganizationID
	return &org
}

// ClearJobWorkspace removes a consumed hint.
func ClearJobWorkspace(db *gorm.DB, jobID uuid.UUID) {
	db.Where("job_id = ?", jobID).Delete(&models.JobWorkspaceHint{})
}

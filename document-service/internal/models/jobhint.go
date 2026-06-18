package models

import (
	"time"

	"github.com/google/uuid"
)

// JobWorkspaceHint records that a processing job was started while a given
// organization workspace was active, so the finalize subscriber can file the
// resulting document into that org. Set by the client at job creation
// (RBAC-checked) and consumed (deleted) when the job completes.
type JobWorkspaceHint struct {
	JobID          uuid.UUID `gorm:"type:uuid;primaryKey" json:"jobId"`
	UserID         uuid.UUID `gorm:"type:uuid;not null" json:"userId"`
	OrganizationID uuid.UUID `gorm:"type:uuid;not null" json:"organizationId"`
	CreatedAt      time.Time `gorm:"default:CURRENT_TIMESTAMP" json:"createdAt"`
}

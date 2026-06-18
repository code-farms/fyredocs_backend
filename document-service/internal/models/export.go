package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Export status values.
const (
	ExportQueued     = "queued"
	ExportProcessing = "processing"
	ExportReady      = "ready"
	ExportFailed     = "failed"
)

// Export is an asynchronously-generated artifact of a user's documents (CSV or
// JSON metadata in v1). The artifact bytes live in Content; for large/binary
// exports this moves to object storage later.
type Export struct {
	ID             uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	UserID         uuid.UUID      `gorm:"type:uuid;not null;index:idx_export_user_created,priority:1" json:"userId"`
	OrganizationID *uuid.UUID     `gorm:"type:uuid" json:"organizationId,omitempty"`
	Format         string         `gorm:"type:text;not null" json:"format"`
	Status         string         `gorm:"type:text;not null;default:'queued'" json:"status"`
	FileName       string         `gorm:"type:text" json:"fileName,omitempty"`
	ContentType    string         `gorm:"type:text" json:"-"`
	Content        []byte         `gorm:"type:bytea" json:"-"`
	DocumentCount  int            `gorm:"default:0" json:"documentCount"`
	Filters        datatypes.JSON `gorm:"type:jsonb" json:"filters,omitempty"`
	Error          string         `gorm:"type:text" json:"error,omitempty"`
	CreatedAt      time.Time      `gorm:"index:idx_export_user_created,priority:2;default:CURRENT_TIMESTAMP" json:"createdAt"`
	CompletedAt    *time.Time     `json:"completedAt,omitempty"`
}

func (e *Export) BeforeCreate(tx *gorm.DB) error {
	if e.ID == uuid.Nil {
		e.ID = uuid.Must(uuid.NewV7())
	}
	return nil
}

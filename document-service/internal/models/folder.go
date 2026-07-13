package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Folder is a user-owned, nestable container for documents.
type Folder struct {
	ID             uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	UserID         uuid.UUID      `gorm:"type:uuid;not null;index" json:"userId"`
	OrganizationID *uuid.UUID     `gorm:"type:uuid;index" json:"organizationId,omitempty"`
	ParentID       *uuid.UUID     `gorm:"type:uuid;index" json:"parentId,omitempty"`
	Name           string         `gorm:"type:text;not null" json:"name"`
	CreatedAt      time.Time      `gorm:"default:CURRENT_TIMESTAMP" json:"createdAt"`
	UpdatedAt      time.Time      `gorm:"default:CURRENT_TIMESTAMP" json:"updatedAt"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`
}

// BeforeCreate assigns a time-ordered UUIDv7 primary key when one was not set.
func (f *Folder) BeforeCreate(tx *gorm.DB) error {
	if f.ID == uuid.Nil {
		f.ID = uuid.Must(uuid.NewV7())
	}
	return nil
}

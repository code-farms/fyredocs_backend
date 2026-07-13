package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Tag is a label scoped to a personal library or an organization. Uniqueness is
// enforced per scope via partial indexes (see Migrate): (user_id, name) for
// personal tags and (organization_id, name) for org tags.
type Tag struct {
	ID             uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	UserID         uuid.UUID  `gorm:"type:uuid;not null;index" json:"userId"`
	OrganizationID *uuid.UUID `gorm:"type:uuid;index" json:"organizationId,omitempty"`
	Name           string     `gorm:"type:text;not null" json:"name"`
	Color          string     `gorm:"type:text" json:"color,omitempty"`
	CreatedAt      time.Time  `gorm:"default:CURRENT_TIMESTAMP" json:"createdAt"`
}

// BeforeCreate assigns a time-ordered UUIDv7 primary key when one was not set.
func (t *Tag) BeforeCreate(tx *gorm.DB) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.Must(uuid.NewV7())
	}
	return nil
}

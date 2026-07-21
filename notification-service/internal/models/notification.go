package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Notification is an in-app message for a user. ReadAt nil = unread.
type Notification struct {
	ID          uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	UserID      uuid.UUID  `gorm:"type:uuid;not null" json:"userId"`
	Type        string     `gorm:"type:text;not null" json:"type"`
	Title       string     `gorm:"type:text;not null" json:"title"`
	Body        string     `gorm:"type:text" json:"body,omitempty"`
	Link        string     `gorm:"type:text" json:"link,omitempty"`
	SourceJobID *uuid.UUID `gorm:"type:uuid" json:"-"`
	ReadAt      *time.Time `json:"readAt,omitempty"`
	CreatedAt   time.Time  `gorm:"default:CURRENT_TIMESTAMP" json:"createdAt"`
}

// BeforeCreate assigns a time-ordered UUIDv7 primary key when one was not set.
func (n *Notification) BeforeCreate(tx *gorm.DB) error {
	if n.ID == uuid.Nil {
		n.ID = uuid.Must(uuid.NewV7())
	}
	return nil
}

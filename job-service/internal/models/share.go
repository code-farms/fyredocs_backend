package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Share is an expiring public download link for one user file (Phase 6).
//
// Unlike job outputs, a share OWNS its object: creation copies the uploaded
// bytes to the outputs bucket under shares/{id}/..., detaching them from the
// upload (and any job) lifecycle. The cleanup sweep removes the object and
// the row once the share expires or is revoked.
//
// Security: only the SHA-256 hex of the 256-bit redeem token is stored — a
// database leak does not leak working links. TokenHash, ObjectKey and UserID
// never serialize to JSON.
type Share struct {
	ID            uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	TokenHash     string     `gorm:"type:text;not null;uniqueIndex:idx_shares_token_hash" json:"-"`
	ObjectKey     string     `gorm:"type:text;not null" json:"-"`
	FileName      string     `gorm:"type:text;not null" json:"fileName"`
	ContentType   *string    `gorm:"type:text" json:"contentType,omitempty"`
	FileSize      int64      `gorm:"not null;default:0" json:"fileSize"`
	UserID        *uuid.UUID `gorm:"type:uuid;index" json:"-"`
	ExpiresAt     time.Time  `gorm:"not null;index" json:"expiresAt"`
	RevokedAt     *time.Time `json:"revokedAt,omitempty"`
	DownloadCount int64      `gorm:"not null;default:0" json:"downloadCount"`
	CreatedAt     time.Time  `gorm:"default:CURRENT_TIMESTAMP" json:"createdAt"`
}

// BeforeCreate assigns a time-ordered UUIDv7 primary key when one was not set.
func (s *Share) BeforeCreate(tx *gorm.DB) (err error) {
	if s.ID == uuid.Nil {
		s.ID = uuid.Must(uuid.NewV7())
	}
	return nil
}

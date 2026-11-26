// Package models owns the identity schema: users, their sessions, the
// subscription plan catalogue, and password-reset tokens.
//
// auth-service is the platform's only token issuer, so this package is the
// authority on who a caller is and what plan they hold. Other services never
// read these tables — they receive identity as gateway-injected headers and
// fetch plan data over the internal API. Session rows back refresh-token
// rotation and reuse detection (see refresh_rotation.go), which is why a
// session is a persisted row rather than purely a signed claim.
package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// User is the authoritative account record owned by auth-service. The password
// hash is never serialized (json:"-"); other services receive only the identity
// claims carried in the issued token.
type User struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	Email        string    `gorm:"type:text;unique;not null" json:"email"`
	FullName     string    `gorm:"type:text" json:"fullName,omitempty"`
	Phone        string    `gorm:"type:text" json:"phone,omitempty"`
	Country      string    `gorm:"type:text" json:"country,omitempty"`
	ImageURL     string    `gorm:"type:text" json:"imageUrl,omitempty"`
	PasswordHash string    `gorm:"type:text;not null" json:"-"`
	PlanName     string    `gorm:"type:text;not null;default:'free'" json:"planName"`
	Role         string    `gorm:"type:text;not null;default:'user'" json:"role"`
	CreatedAt    time.Time `gorm:"default:CURRENT_TIMESTAMP" json:"createdAt"`
}

// BeforeCreate assigns a time-ordered UUIDv7 primary key when one was not set,
// keeping newly inserted rows roughly index-sequential.
func (u *User) BeforeCreate(tx *gorm.DB) (err error) {
	if u.ID == uuid.Nil {
		u.ID = uuid.Must(uuid.NewV7())
	}
	return nil
}

// AuthMetadata records the identity provider and last-login timestamp for a
// user, allowing a single account to be linked to multiple auth providers.
type AuthMetadata struct {
	ID          uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	UserID      uuid.UUID  `gorm:"type:uuid;not null;index;constraint:OnDelete:CASCADE" json:"userId"`
	User        *User      `gorm:"foreignKey:UserID" json:"-"`
	Provider    string     `gorm:"type:text;not null" json:"provider"`
	Subject     string     `gorm:"type:text;not null" json:"subject"`
	LastLoginAt *time.Time `gorm:"" json:"lastLoginAt,omitempty"`
	CreatedAt   time.Time  `gorm:"default:CURRENT_TIMESTAMP" json:"createdAt"`
}

// BeforeCreate assigns a time-ordered UUIDv7 primary key when one was not set.
func (a *AuthMetadata) BeforeCreate(tx *gorm.DB) (err error) {
	if a.ID == uuid.Nil {
		a.ID = uuid.Must(uuid.NewV7())
	}
	return nil
}

// SubscriptionPlan defines the file-size, per-job, and retention limits that a
// named plan (guest, free, pro) grants. The database row is the single source of
// these limits: auth-service reads it, returns it to clients (login, /auth/me,
// /auth/plans) and publishes it to the gateway's plan cache, so the web app, the
// mobile app, the gateway and job-service all enforce the same numbers.
type SubscriptionPlan struct {
	ID             uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	Name           string    `gorm:"type:text;unique;not null" json:"name"`
	MaxFileSizeMB  int       `gorm:"not null" json:"maxFileSizeMb"`
	MaxFilesPerJob int       `gorm:"not null" json:"maxFilesPerJob"`
	// MaxJobInputMB caps the total size of all files in one job. Must stay at or
	// below what the PDF workers can hold (300 MB: total input × 3 within their
	// 900 MB tmpfs budget), or jobs pass upload checks and fail in the worker.
	MaxJobInputMB int `gorm:"not null;default:300" json:"maxJobInputMb"`
	// MaxOCRPages caps the pages of one OCR job (ocr-pdf). The optimize-pdf
	// worker enforces it after counting pages, before any OCR work starts.
	MaxOCRPages   int       `gorm:"column:max_ocr_pages;not null;default:30" json:"maxOcrPages"`
	RetentionDays int       `gorm:"not null" json:"retentionDays"`
	CreatedAt     time.Time `gorm:"default:CURRENT_TIMESTAMP" json:"createdAt"`
}

// BeforeCreate assigns a time-ordered UUIDv7 primary key when one was not set.
func (p *SubscriptionPlan) BeforeCreate(tx *gorm.DB) (err error) {
	if p.ID == uuid.Nil {
		p.ID = uuid.Must(uuid.NewV7())
	}
	return nil
}

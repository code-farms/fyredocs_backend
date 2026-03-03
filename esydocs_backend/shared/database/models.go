package database

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type User struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	Email        string    `gorm:"type:text;unique;not null" json:"email"`
	FullName     string    `gorm:"type:text" json:"fullName,omitempty"`
	Phone        string    `gorm:"type:text" json:"phone,omitempty"`
	Country      string    `gorm:"type:text" json:"country,omitempty"`
	ImageURL     string    `gorm:"type:text" json:"imageUrl,omitempty"`
	PasswordHash string    `gorm:"type:text;not null" json:"-"`
	CreatedAt    time.Time `gorm:"default:CURRENT_TIMESTAMP" json:"createdAt"`
}

func (u *User) BeforeCreate(tx *gorm.DB) (err error) {
	if u.ID == uuid.Nil {
		u.ID = uuid.New()
	}
	return nil
}

type AuthMetadata struct {
	ID          uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	UserID      uuid.UUID  `gorm:"type:uuid;not null;index" json:"userId"`
	Provider    string     `gorm:"type:text;not null" json:"provider"`
	Subject     string     `gorm:"type:text;not null" json:"subject"`
	LastLoginAt *time.Time `gorm:"" json:"lastLoginAt,omitempty"`
	CreatedAt   time.Time  `gorm:"default:CURRENT_TIMESTAMP" json:"createdAt"`
}

func (a *AuthMetadata) BeforeCreate(tx *gorm.DB) (err error) {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	return nil
}

type SubscriptionPlan struct {
	ID            uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	Name          string    `gorm:"type:text;unique;not null" json:"name"`
	MaxFileSizeMB int       `gorm:"not null" json:"maxFileSizeMb"`
	RetentionDays int       `gorm:"not null" json:"retentionDays"`
	CreatedAt     time.Time `gorm:"default:CURRENT_TIMESTAMP" json:"createdAt"`
}

func (p *SubscriptionPlan) BeforeCreate(tx *gorm.DB) (err error) {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	return nil
}

// Fix #12: Progress is int (0-100), FileSize is int64 (bytes)
// Fix #22: Added index tags to Status and ToolType for query performance
type ProcessingJob struct {
	ID            uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	UserID        *uuid.UUID     `gorm:"type:uuid;index" json:"userId,omitempty"`
	ToolType      string         `gorm:"type:text;not null;index" json:"toolType"`
	Status        string         `gorm:"type:text;not null;default:'queued';index" json:"status"`
	Progress      int            `gorm:"default:0" json:"progress"`
	FileName      string         `gorm:"type:text;not null" json:"fileName"`
	FileSize      int64          `gorm:"default:0" json:"fileSize"`
	FailureReason *string        `gorm:"type:text" json:"failureReason,omitempty"`
	Metadata      datatypes.JSON `gorm:"type:jsonb" json:"metadata,omitempty"`
	CreatedAt     time.Time      `gorm:"default:CURRENT_TIMESTAMP" json:"createdAt"`
	UpdatedAt     time.Time      `gorm:"default:CURRENT_TIMESTAMP" json:"updatedAt"`
	CompletedAt   *time.Time     `json:"completedAt,omitempty"`
	ExpiresAt     *time.Time     `gorm:"index" json:"expiresAt,omitempty"`
}

func (job *ProcessingJob) BeforeCreate(tx *gorm.DB) (err error) {
	if job.ID == uuid.Nil {
		job.ID = uuid.New()
	}
	return nil
}

type FileMetadata struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	JobID        uuid.UUID `gorm:"type:uuid;index;not null" json:"jobId"`
	Kind         string    `gorm:"type:text;not null" json:"kind"`
	OriginalName string    `gorm:"type:text;not null" json:"originalName"`
	Path         string    `gorm:"type:text;not null" json:"path"`
	SizeBytes    int64     `gorm:"not null" json:"sizeBytes"`
	CreatedAt    time.Time `gorm:"default:CURRENT_TIMESTAMP" json:"createdAt"`
}

func (f *FileMetadata) BeforeCreate(tx *gorm.DB) (err error) {
	if f.ID == uuid.Nil {
		f.ID = uuid.New()
	}
	return nil
}

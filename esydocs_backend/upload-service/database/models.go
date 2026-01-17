package database

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type User struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey"`
	Email        string    `gorm:"type:text;unique;not null"`
	PasswordHash string    `gorm:"type:text;not null"`
	CreatedAt    time.Time `gorm:"default:CURRENT_TIMESTAMP"`
}

func (u *User) BeforeCreate(tx *gorm.DB) (err error) {
	if u.ID == uuid.Nil {
		u.ID = uuid.New()
	}
	return nil
}

type AuthMetadata struct {
	ID          uuid.UUID  `gorm:"type:uuid;primaryKey"`
	UserID      uuid.UUID  `gorm:"type:uuid;not null;index"`
	Provider    string     `gorm:"type:text;not null"`
	Subject     string     `gorm:"type:text;not null"`
	LastLoginAt *time.Time `gorm:""`
	CreatedAt   time.Time  `gorm:"default:CURRENT_TIMESTAMP"`
}

func (a *AuthMetadata) BeforeCreate(tx *gorm.DB) (err error) {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	return nil
}

type SubscriptionPlan struct {
	ID            uuid.UUID `gorm:"type:uuid;primaryKey"`
	Name          string    `gorm:"type:text;unique;not null"`
	MaxFileSizeMB int       `gorm:"not null"`
	RetentionDays int       `gorm:"not null"`
	CreatedAt     time.Time `gorm:"default:CURRENT_TIMESTAMP"`
}

func (p *SubscriptionPlan) BeforeCreate(tx *gorm.DB) (err error) {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	return nil
}

type ProcessingJob struct {
	ID            uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	UserID        *uuid.UUID     `gorm:"type:uuid;index" json:"userId,omitempty"`
	ToolType      string         `gorm:"type:text;not null" json:"toolType"`
	Status        string         `gorm:"type:text;not null;default:'queued'" json:"status"`
	Progress      string         `gorm:"type:text;default:'0'" json:"progress"`
	FileName      string         `gorm:"type:text;not null" json:"fileName"`
	FileSize      string         `gorm:"type:text;not null" json:"fileSize"`
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
	ID           uuid.UUID `gorm:"type:uuid;primaryKey"`
	JobID        uuid.UUID `gorm:"type:uuid;index;not null"`
	Kind         string    `gorm:"type:text;not null"`
	OriginalName string    `gorm:"type:text;not null"`
	Path         string    `gorm:"type:text;not null"`
	SizeBytes    int64     `gorm:"not null"`
	CreatedAt    time.Time `gorm:"default:CURRENT_TIMESTAMP"`
}

func (f *FileMetadata) BeforeCreate(tx *gorm.DB) (err error) {
	if f.ID == uuid.Nil {
		f.ID = uuid.New()
	}
	return nil
}

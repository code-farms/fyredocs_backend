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
	ID            uuid.UUID      `gorm:"type:uuid;primaryKey"`
	UserID        *uuid.UUID     `gorm:"type:uuid;index"`
	ToolType      string         `gorm:"type:text;not null"`
	Status        string         `gorm:"type:text;not null;default:'queued'"`
	Progress      string         `gorm:"type:text;default:'0'"`
	FileName      string         `gorm:"type:text;not null"`
	FileSize      string         `gorm:"type:text;not null"`
	FailureReason *string        `gorm:"type:text"`
	Metadata      datatypes.JSON `gorm:"type:jsonb"`
	CreatedAt     time.Time      `gorm:"default:CURRENT_TIMESTAMP"`
	UpdatedAt     time.Time      `gorm:"default:CURRENT_TIMESTAMP"`
	CompletedAt   *time.Time
	ExpiresAt     *time.Time `gorm:"index"`
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

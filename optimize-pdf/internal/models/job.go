package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ProcessingJob is this service's local view of a job it processes. The job
// lifecycle is owned by job-service; this row tracks status and progress for
// the work performed here.
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

// BeforeCreate assigns a UUID primary key when one was not set.
func (job *ProcessingJob) BeforeCreate(tx *gorm.DB) (err error) {
	if job.ID == uuid.Nil {
		job.ID = uuid.New()
	}
	return nil
}

// FileMetadata records one input or output file for a job, keyed by Kind.
type FileMetadata struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	JobID        uuid.UUID `gorm:"type:uuid;index;not null" json:"jobId"`
	Kind         string    `gorm:"type:text;not null" json:"kind"`
	OriginalName string    `gorm:"type:text;not null" json:"originalName"`
	Path         string    `gorm:"type:text;not null" json:"-"`
	SizeBytes    int64     `gorm:"not null" json:"sizeBytes"`
	CreatedAt    time.Time `gorm:"default:CURRENT_TIMESTAMP" json:"createdAt"`
}

// BeforeCreate assigns a UUID primary key when one was not set.
func (f *FileMetadata) BeforeCreate(tx *gorm.DB) (err error) {
	if f.ID == uuid.Nil {
		f.ID = uuid.New()
	}
	return nil
}

package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ProcessingJob is a unit of asynchronous PDF/document work. It tracks the
// tool, status, and progress of one job; the associated input and output files
// are recorded as FileMetadata rows. ExpiresAt drives the TTL cleanup sweep.
type ProcessingJob struct {
	ID            uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	UserID        *uuid.UUID     `gorm:"type:uuid" json:"userId,omitempty"`
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

// BeforeCreate assigns a time-ordered UUIDv7 primary key when one was not set.
func (job *ProcessingJob) BeforeCreate(tx *gorm.DB) (err error) {
	if job.ID == uuid.Nil {
		job.ID = uuid.Must(uuid.NewV7())
	}
	return nil
}

// FileMetadata records one input or output file for a job. Kind ("input" or
// "output") selects the object-storage bucket; see Path for the key layout.
type FileMetadata struct {
	ID            uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	JobID         uuid.UUID      `gorm:"type:uuid;index;not null;constraint:OnDelete:CASCADE" json:"jobId"`
	ProcessingJob *ProcessingJob `gorm:"foreignKey:JobID" json:"-"`
	Kind          string         `gorm:"type:text;not null" json:"kind"`
	OriginalName  string         `gorm:"type:text;not null" json:"originalName"`
	// Path is the S3/MinIO object key of the file, NOT a filesystem path.
	// The bucket is derived from Kind: "input" rows live in the uploads
	// bucket (uploads/{uploadId|jobId}/{fileName}), "output" rows in the
	// outputs bucket. Rows created before the presigned-upload migration may
	// still hold absolute disk paths (they start with "/"); those are treated
	// as expired and are skipped on delete / rejected on download.
	Path      string    `gorm:"type:text;not null" json:"-"`
	SizeBytes int64     `gorm:"not null" json:"sizeBytes"`
	CreatedAt time.Time `gorm:"default:CURRENT_TIMESTAMP" json:"createdAt"`
}

// BeforeCreate assigns a time-ordered UUIDv7 primary key when one was not set.
func (f *FileMetadata) BeforeCreate(tx *gorm.DB) (err error) {
	if f.ID == uuid.Nil {
		f.ID = uuid.Must(uuid.NewV7())
	}
	return nil
}

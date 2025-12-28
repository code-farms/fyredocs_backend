package database

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type User struct {
	ID       uuid.UUID `gorm:"type:varchar(36);primary_key;"`
	Username string    `gorm:"type:text;not null;unique"`
	Password string    `gorm:"type:text;not null"`
}

func (u *User) BeforeCreate(tx *gorm.DB) (err error) {
	u.ID = uuid.New()
	return
}

type ProcessingJob struct {
	ID            uuid.UUID      `gorm:"type:varchar(36);primary_key;"`
	UserID        *uuid.UUID     `gorm:"type:varchar(36)"` // Pointer to allow nulls
	FileName      string         `gorm:"type:text;not null"`
	FileSize      string         `gorm:"type:text;not null"`
	ToolType      string         `gorm:"type:text;not null"`
	Status        string         `gorm:"type:text;not null;default:'pending'"`
	Progress      string         `gorm:"type:text;default:'0'"`
	InputFileUrl  *string        `gorm:"type:text"`
	OutputFileUrl *string        `gorm:"type:text"`
	Metadata      datatypes.JSON `gorm:"type:jsonb"`
	CreatedAt     time.Time      `gorm:"default:CURRENT_TIMESTAMP"`
	CompletedAt   *time.Time
}

func (job *ProcessingJob) BeforeCreate(tx *gorm.DB) (err error) {
	job.ID = uuid.New()
	return
}

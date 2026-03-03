package database

import (
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestProcessingJobBeforeCreate(t *testing.T) {
	job := &ProcessingJob{}
	if job.ID != uuid.Nil {
		t.Error("new job should have zero UUID")
	}
	_ = job.BeforeCreate(&gorm.DB{})
	if job.ID == uuid.Nil {
		t.Error("BeforeCreate should set UUID")
	}
}

func TestFileMetadataBeforeCreate(t *testing.T) {
	fm := &FileMetadata{}
	if fm.ID != uuid.Nil {
		t.Error("new FileMetadata should have zero UUID")
	}
	_ = fm.BeforeCreate(&gorm.DB{})
	if fm.ID == uuid.Nil {
		t.Error("BeforeCreate should set UUID")
	}
}

func TestUserBeforeCreate(t *testing.T) {
	u := &User{}
	if u.ID != uuid.Nil {
		t.Error("new User should have zero UUID")
	}
	_ = u.BeforeCreate(&gorm.DB{})
	if u.ID == uuid.Nil {
		t.Error("BeforeCreate should set UUID")
	}
}

func TestAuthMetadataBeforeCreate(t *testing.T) {
	a := &AuthMetadata{}
	if a.ID != uuid.Nil {
		t.Error("new AuthMetadata should have zero UUID")
	}
	_ = a.BeforeCreate(&gorm.DB{})
	if a.ID == uuid.Nil {
		t.Error("BeforeCreate should set UUID")
	}
}

func TestSubscriptionPlanBeforeCreate(t *testing.T) {
	p := &SubscriptionPlan{}
	if p.ID != uuid.Nil {
		t.Error("new SubscriptionPlan should have zero UUID")
	}
	_ = p.BeforeCreate(&gorm.DB{})
	if p.ID == uuid.Nil {
		t.Error("BeforeCreate should set UUID")
	}
}

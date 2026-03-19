package models

import (
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestAnalyticsEvent_BeforeCreate_GeneratesID(t *testing.T) {
	event := &AnalyticsEvent{}
	if err := event.BeforeCreate(&gorm.DB{}); err != nil {
		t.Fatalf("BeforeCreate returned error: %v", err)
	}
	if event.ID == uuid.Nil {
		t.Fatal("expected ID to be generated, got nil UUID")
	}
}

func TestAnalyticsEvent_BeforeCreate_PreservesExistingID(t *testing.T) {
	existing := uuid.New()
	event := &AnalyticsEvent{ID: existing}
	if err := event.BeforeCreate(&gorm.DB{}); err != nil {
		t.Fatalf("BeforeCreate returned error: %v", err)
	}
	if event.ID != existing {
		t.Fatalf("expected ID %s, got %s", existing, event.ID)
	}
}

func TestDailyMetric_BeforeCreate_GeneratesID(t *testing.T) {
	metric := &DailyMetric{}
	if err := metric.BeforeCreate(&gorm.DB{}); err != nil {
		t.Fatalf("BeforeCreate returned error: %v", err)
	}
	if metric.ID == uuid.Nil {
		t.Fatal("expected ID to be generated, got nil UUID")
	}
}

func TestDailyMetric_BeforeCreate_PreservesExistingID(t *testing.T) {
	existing := uuid.New()
	metric := &DailyMetric{ID: existing}
	if err := metric.BeforeCreate(&gorm.DB{}); err != nil {
		t.Fatalf("BeforeCreate returned error: %v", err)
	}
	if metric.ID != existing {
		t.Fatalf("expected ID %s, got %s", existing, metric.ID)
	}
}

package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// AnalyticsEvent stores individual analytics events for detailed querying.
type AnalyticsEvent struct {
	ID          uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	EventType   string         `gorm:"type:text;not null;index:idx_event_type" json:"eventType"`
	UserID      *uuid.UUID     `gorm:"type:uuid;index:idx_event_user" json:"userId,omitempty"`
	JobID       *uuid.UUID     `gorm:"type:uuid;index:idx_event_job" json:"jobId,omitempty"`
	IsGuest     bool           `gorm:"default:false" json:"isGuest"`
	ToolType    string         `gorm:"type:text;index:idx_event_tool" json:"toolType,omitempty"`
	PlanName    string         `gorm:"type:text" json:"planName,omitempty"`
	FileSize    int64          `gorm:"default:0" json:"fileSize,omitempty"`
	Metadata    datatypes.JSON `gorm:"type:jsonb" json:"metadata,omitempty"`
	CreatedAt   time.Time      `gorm:"index:idx_event_created;default:CURRENT_TIMESTAMP" json:"createdAt"`
	PersistedAt time.Time      `gorm:"default:CURRENT_TIMESTAMP" json:"persistedAt"`
}

// BeforeCreate assigns a time-ordered UUIDv7 primary key when one was not set.
func (e *AnalyticsEvent) BeforeCreate(tx *gorm.DB) error {
	if e.ID == uuid.Nil {
		e.ID = uuid.Must(uuid.NewV7())
	}
	return nil
}

// DailyMetric stores pre-aggregated daily metrics for fast dashboard queries.
type DailyMetric struct {
	ID          uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	Date        time.Time      `gorm:"type:date;uniqueIndex:idx_daily_metric;not null" json:"date"`
	MetricName  string         `gorm:"type:text;uniqueIndex:idx_daily_metric;not null" json:"metricName"`
	MetricValue float64        `gorm:"not null" json:"metricValue"`
	Dimensions  datatypes.JSON `gorm:"type:jsonb" json:"dimensions,omitempty"`
	CreatedAt   time.Time      `gorm:"default:CURRENT_TIMESTAMP" json:"createdAt"`
	UpdatedAt   time.Time      `gorm:"default:CURRENT_TIMESTAMP" json:"updatedAt"`
}

// BeforeCreate assigns a time-ordered UUIDv7 primary key when one was not set.
func (d *DailyMetric) BeforeCreate(tx *gorm.DB) error {
	if d.ID == uuid.Nil {
		d.ID = uuid.Must(uuid.NewV7())
	}
	return nil
}

// APIMetricSample is one periodic sample of gateway HTTP metrics. Counts are
// per-interval deltas (current cumulative scrape minus the previous), latency
// columns are the overall snapshot at sample time. Written by the API metrics
// sampler and read by the /admin/metrics/api-trends handler.
type APIMetricSample struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	SampledAt    time.Time `gorm:"index:idx_api_sample_time;not null" json:"sampledAt"`
	Requests     int64     `gorm:"default:0" json:"requests"`
	ClientErrors int64     `gorm:"default:0" json:"clientErrors"`
	ServerErrors int64     `gorm:"default:0" json:"serverErrors"`
	Timeouts     int64     `gorm:"default:0" json:"timeouts"`
	AvgMs        float64   `gorm:"default:0" json:"avgMs"`
	P50Ms        float64   `gorm:"default:0" json:"p50Ms"`
	P95Ms        float64   `gorm:"default:0" json:"p95Ms"`
	P99Ms        float64   `gorm:"default:0" json:"p99Ms"`
	CreatedAt    time.Time `gorm:"default:CURRENT_TIMESTAMP" json:"createdAt"`
}

// BeforeCreate assigns a time-ordered UUIDv7 primary key when one was not set.
func (s *APIMetricSample) BeforeCreate(tx *gorm.DB) error {
	if s.ID == uuid.Nil {
		s.ID = uuid.Must(uuid.NewV7())
	}
	return nil
}

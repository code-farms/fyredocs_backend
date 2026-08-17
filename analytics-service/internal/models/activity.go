package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UserActivity is one audit-trail row: who did what, to which entity, from
// which platform, with what outcome. Unlike AnalyticsEvent (lossy metrics),
// rows here arrive through an idempotent write path — ClientEventID dedupes
// client retries — and are never updated after insert; the only delete path
// is the retention sweep.
type UserActivity struct {
	ID uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	// ClientEventID is the client-minted idempotency key. NULL for
	// server-origin rows (subscriber mappings), which are exactly-once by
	// construction and never retried by a client.
	ClientEventID *string        `gorm:"type:text" json:"clientEventId,omitempty"`
	UserID        *uuid.UUID     `gorm:"type:uuid" json:"userId,omitempty"`
	IsGuest       bool           `gorm:"not null;default:false" json:"isGuest"`
	SessionID     string         `gorm:"type:text" json:"sessionId,omitempty"`
	EventType     string         `gorm:"type:text;not null" json:"eventType"`
	FeatureID     string         `gorm:"type:text" json:"featureId,omitempty"`
	ToolID        string         `gorm:"type:text" json:"toolId,omitempty"`
	Action        string         `gorm:"type:text" json:"action,omitempty"`
	EntityType    string         `gorm:"type:text" json:"entityType,omitempty"`
	EntityID      string         `gorm:"type:text" json:"entityId,omitempty"`
	DocumentID    string         `gorm:"type:text" json:"documentId,omitempty"`
	Status        string         `gorm:"type:text;not null;default:success" json:"status"`
	FailureReason string         `gorm:"type:text" json:"failureReason,omitempty"`
	ErrorCode     string         `gorm:"type:text" json:"errorCode,omitempty"`
	OldValue      datatypes.JSON `gorm:"type:jsonb" json:"oldValue,omitempty"`
	NewValue      datatypes.JSON `gorm:"type:jsonb" json:"newValue,omitempty"`
	Metadata      datatypes.JSON `gorm:"type:jsonb" json:"metadata,omitempty"`
	Platform      string         `gorm:"type:text" json:"platform,omitempty"`
	DeviceType    string         `gorm:"type:text" json:"deviceType,omitempty"`
	AppVersion    string         `gorm:"type:text" json:"appVersion,omitempty"`
	CorrelationID string         `gorm:"type:text" json:"correlationId,omitempty"`
	// OccurredAt is the client's clock (clamped at ingest); CreatedAt is ours.
	// Feed ordering uses OccurredAt, retention uses CreatedAt, so a client with
	// a skewed clock can misplace its own rows but cannot dodge retention.
	OccurredAt time.Time `gorm:"not null" json:"occurredAt"`
	CreatedAt  time.Time `gorm:"not null;default:CURRENT_TIMESTAMP" json:"createdAt"`
}

// TableName pins the table GORM would otherwise pluralize to "user_activities",
// which would diverge from the 00002 migration.
func (UserActivity) TableName() string { return "user_activity" }

// BeforeCreate assigns a time-ordered UUIDv7 primary key when one was not set.
func (a *UserActivity) BeforeCreate(tx *gorm.DB) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.Must(uuid.NewV7())
	}
	return nil
}

// InsertUserActivities writes a batch idempotently and reports how many rows
// were actually inserted. ON CONFLICT targets the partial unique index on
// client_event_id, so a client retrying a delivered batch gets inserted=0
// instead of duplicate rows; RowsAffected is what makes the "deduplicated"
// count in the ingest response honest rather than assumed.
func InsertUserActivities(db *gorm.DB, rows []UserActivity) (inserted int64, err error) {
	if len(rows) == 0 {
		return 0, nil
	}
	res := db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "client_event_id"}},
		TargetWhere: clause.Where{Exprs: []clause.Expression{
			clause.Expr{SQL: "client_event_id IS NOT NULL"},
		}},
		DoNothing: true,
	}).Create(&rows)
	return res.RowsAffected, res.Error
}

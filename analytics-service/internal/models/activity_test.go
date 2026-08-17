package models

import (
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestUserActivity_BeforeCreate_GeneratesID(t *testing.T) {
	row := &UserActivity{}
	if err := row.BeforeCreate(&gorm.DB{}); err != nil {
		t.Fatalf("BeforeCreate returned error: %v", err)
	}
	if row.ID == uuid.Nil {
		t.Fatal("expected ID to be generated, got nil UUID")
	}
}

func TestUserActivity_BeforeCreate_PreservesExistingID(t *testing.T) {
	existing := uuid.New()
	row := &UserActivity{ID: existing}
	if err := row.BeforeCreate(&gorm.DB{}); err != nil {
		t.Fatalf("BeforeCreate returned error: %v", err)
	}
	if row.ID != existing {
		t.Fatalf("expected ID %s, got %s", existing, row.ID)
	}
}

// TestUserActivity_TableName guards the migration/model contract: GORM's
// default pluralization would be "user_activities", silently diverging from
// the table 00002_user_activity.sql creates.
func TestUserActivity_TableName(t *testing.T) {
	if got := (UserActivity{}).TableName(); got != "user_activity" {
		t.Fatalf("TableName() = %q, want %q", got, "user_activity")
	}
}

// TestInsertUserActivities_EmptyBatch matters to the ingest handler: a batch
// where every event was rejected reaches the insert with zero rows and must
// not touch the database (the handler may hold a nil handle in tests).
func TestInsertUserActivities_EmptyBatch(t *testing.T) {
	inserted, err := InsertUserActivities(nil, nil)
	if err != nil {
		t.Fatalf("InsertUserActivities(nil, nil) error = %v, want nil", err)
	}
	if inserted != 0 {
		t.Fatalf("inserted = %d, want 0", inserted)
	}
}

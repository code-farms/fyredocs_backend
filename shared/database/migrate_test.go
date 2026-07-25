package database

import (
	"strings"
	"testing"
	"time"
)

func TestHistoryTableIsPerServiceAndValid(t *testing.T) {
	cases := map[string]string{
		"auth-service":         "goose_db_version_auth_service",
		"job-service":          "goose_db_version_job_service",
		"notification-service": "goose_db_version_notification_service",
		" analytics-service ":  "goose_db_version_analytics_service",
	}
	for service, want := range cases {
		got := historyTable(service)
		if got != want {
			t.Errorf("historyTable(%q) = %q, want %q", service, got, want)
		}
		// Hyphens would need quoting in SQL; the table name must be a bare
		// identifier.
		if strings.ContainsAny(got, "-\" ;") {
			t.Errorf("historyTable(%q) = %q contains characters invalid in a bare identifier", service, got)
		}
	}
}

func TestAdvisoryLockIDIsStableAndPerService(t *testing.T) {
	a1 := advisoryLockID("auth-service")
	a2 := advisoryLockID("auth-service")
	if a1 != a2 {
		t.Errorf("lock id is not stable: %d vs %d", a1, a2)
	}
	if a1 < 0 {
		t.Errorf("lock id must be non-negative for readable pg_locks output, got %d", a1)
	}

	// Different services must not serialize behind each other.
	seen := map[int64]string{}
	for _, svc := range []string{
		"auth-service", "job-service", "user-service",
		"document-service", "notification-service", "analytics-service",
	} {
		id := advisoryLockID(svc)
		if other, dup := seen[id]; dup {
			t.Errorf("lock id collision between %q and %q (%d)", svc, other, id)
		}
		seen[id] = svc
	}
}

func TestMigrationTimeoutEnv(t *testing.T) {
	t.Setenv("DB_MIGRATION_TIMEOUT", "")
	if got := migrationTimeout(); got != 10*time.Minute {
		t.Errorf("default = %v, want 10m", got)
	}
	t.Setenv("DB_MIGRATION_TIMEOUT", "2m")
	if got := migrationTimeout(); got != 2*time.Minute {
		t.Errorf("got %v, want 2m", got)
	}
	t.Setenv("DB_MIGRATION_TIMEOUT", "nonsense")
	if got := migrationTimeout(); got != 10*time.Minute {
		t.Errorf("invalid value should fall back to 10m, got %v", got)
	}
}

func TestMigrationDBRequiresAHandle(t *testing.T) {
	t.Setenv("DATABASE_URL_DIRECT", "")
	if _, _, err := migrationDB(nil); err == nil {
		t.Error("expected an error when no GORM handle and no DATABASE_URL_DIRECT")
	}
}

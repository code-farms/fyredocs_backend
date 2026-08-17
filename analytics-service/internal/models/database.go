package models

import (
	"log/slog"
	"os"
	"time"

	"gorm.io/gorm"

	"analytics-service/internal/migrations"

	"fyredocs/shared/config"
	"fyredocs/shared/database"
)

// DB is the package-global GORM handle for analytics-service, initialized by Connect.
var DB *gorm.DB

// PoolConfig aliases the shared pool settings so main() call sites keep
// their existing models.PoolConfig{...} overrides.
type PoolConfig = database.PoolConfig

// servicePoolBase is this service's default pool sizing; positive fields in a
// caller-supplied PoolConfig override it (see database.PoolConfig.WithOverrides).
func servicePoolBase() PoolConfig {
	return PoolConfig{
		MaxOpenConns:    15,
		MaxIdleConns:    5,
		ConnMaxLifetime: 5 * time.Minute,
		ConnMaxIdleTime: 2 * time.Minute,
	}
}

// Connect opens the shared pooled GORM handle against DATABASE_URL and
// fail-fasts on any error. Connection mechanics live in
// fyredocs/shared/database; this service owns only its schema (see Migrate).
func Connect(pool ...PoolConfig) {
	DB = database.MustConnectFromEnv(servicePoolBase(), pool...)
}

// Migrate brings the analytics-service schema up to date: it auto-migrates the
// event and metric tables and adds composite indexes tuned for the dashboard
// queries. It fail-fasts on the core migration but treats index creation as
// best-effort.
func Migrate() {
	// Versioned migrations are the schema authority (see
	// docs/developer/architecture/schema-migrations.md). They run under a
	// per-service Postgres advisory lock, so concurrent replicas serialize
	// instead of racing the same DDL, and a failure aborts startup — a service
	// that could not apply its schema must not serve traffic.
	if config.GetEnvBool("DB_AUTOMIGRATE", false) {
		slog.Warn("DB_AUTOMIGRATE=true — using the legacy GORM AutoMigrate path instead of versioned migrations. Development only; never set this in production.")
		legacyAutoMigrate()

		return
	}

	if err := database.RunMigrations(DB, "analytics-service", migrations.FS); err != nil {
		slog.Error("Database migration failed", "error", err)
		os.Exit(1)
	}
	slog.Info("Database migration completed")

}

// legacyAutoMigrate is the pre-migrations GORM bootstrap, kept behind
// DB_AUTOMIGRATE for local development against a scratch database (and used by
// scripts/gen-migration-baseline.sh to regenerate the baseline). It is NOT the
// schema authority: changes must land as numbered migrations.
func legacyAutoMigrate() {
	if err := DB.AutoMigrate(
		&AnalyticsEvent{},
		&DailyMetric{},
		&APIMetricSample{},
		&UserActivity{},
	); err != nil {
		slog.Error("Database migration failed", "error", err)
		os.Exit(1)
	}

	compositeIndexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_event_user_type_created ON analytics_events (user_id, event_type, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_event_created_user ON analytics_events (created_at, user_id) WHERE user_id IS NOT NULL AND is_guest = false`,
		`CREATE INDEX IF NOT EXISTS idx_event_job_type ON analytics_events (job_id, event_type) WHERE job_id IS NOT NULL`,
		// Hot admin-metrics / dashboard patterns: filter by one equality col + created_at range/sort.
		`CREATE INDEX IF NOT EXISTS idx_event_type_created ON analytics_events (event_type, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_event_user_created ON analytics_events (user_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_event_tool_created ON analytics_events (tool_type, created_at)`,
		// user_activity indexes mirror 00002_user_activity.sql — GORM tags
		// cannot express the partial unique or DESC forms, so dev parity
		// lives here. NOTE: keep in sync with internal/migrations/00002_user_activity.sql.
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_ua_client_event ON user_activity (client_event_id) WHERE client_event_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_ua_user_created ON user_activity (user_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_ua_document_created ON user_activity (document_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_ua_event_created ON user_activity (event_type, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_ua_session ON user_activity (session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_ua_correlation ON user_activity (correlation_id)`,
	}
	for _, idx := range compositeIndexes {
		if err := DB.Exec(idx).Error; err != nil {
			slog.Warn("Failed to create composite index", "error", err)
		}
	}

	slog.Info("Database migration completed")
}

package models

import (
	"log/slog"
	"os"
	"time"

	"gorm.io/gorm"

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
	if err := DB.AutoMigrate(
		&AnalyticsEvent{},
		&DailyMetric{},
		&APIMetricSample{},
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
	}
	for _, idx := range compositeIndexes {
		if err := DB.Exec(idx).Error; err != nil {
			slog.Warn("Failed to create composite index", "error", err)
		}
	}

	slog.Info("Database migration completed")
}

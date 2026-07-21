package models

import (
	"log/slog"
	"os"
	"time"

	"gorm.io/gorm"

	"fyredocs/shared/database"
)

// DB is the package-global GORM handle for notification-service, initialized by Connect.
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

// Migrate brings the notification-service schema up to date: it auto-migrates
// the notifications table and adds the query and idempotency indexes. It
// fail-fasts on the core migration but treats index creation as best-effort.
func Migrate() {
	if err := DB.AutoMigrate(&Notification{}); err != nil {
		slog.Error("Database migration failed", "error", err)
		os.Exit(1)
	}
	stmts := []string{
		`CREATE INDEX IF NOT EXISTS idx_notif_user_created ON notifications (user_id, created_at DESC)`,
		// One notification per (user, source job) keeps the subscriber idempotent.
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_notif_user_source ON notifications (user_id, source_job_id) WHERE source_job_id IS NOT NULL`,
		// Unread-badge count: user_id = ? AND read_at IS NULL.
		`CREATE INDEX IF NOT EXISTS idx_notif_user_unread ON notifications (user_id) WHERE read_at IS NULL`,
	}
	for _, s := range stmts {
		if err := DB.Exec(s).Error; err != nil {
			slog.Warn("post-migration statement failed", "error", err)
		}
	}
	slog.Info("Database migration completed")
}

package models

import (
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"auth-service/internal/migrations"
	"fyredocs/shared/config"

	"fyredocs/shared/database"
)

// DB is the package-global GORM handle for auth-service, initialized by Connect.
var DB *gorm.DB

// PoolConfig aliases the shared pool settings so main() call sites keep
// their existing models.PoolConfig{...} overrides.
type PoolConfig = database.PoolConfig

// servicePoolBase is this service's default pool sizing; positive fields in a
// caller-supplied PoolConfig override it (see database.PoolConfig.WithOverrides).
func servicePoolBase() PoolConfig {
	return PoolConfig{
		MaxOpenConns:    25,
		MaxIdleConns:    10,
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

// Migrate brings the auth-service schema up to date: it auto-migrates the owned
// tables, adds composite indexes and the role check constraint, and seeds the
// canonical subscription plans. It fail-fasts if the core migration fails but
// treats index/constraint errors as warnings so a partially-migrated database
// can still start.
func Migrate() {
	// Versioned migrations are the schema authority (see
	// docs/developer/architecture/schema-migrations.md). They run under a
	// per-service Postgres advisory lock, so concurrent replicas serialize
	// instead of racing the same DDL, and a failure aborts startup — a service
	// that could not apply its schema must not serve traffic.
	if config.GetEnvBool("DB_AUTOMIGRATE", false) {
		slog.Warn("DB_AUTOMIGRATE=true — using the legacy GORM AutoMigrate path instead of versioned migrations. Development only; never set this in production.")
		legacyAutoMigrate()
		seedPlans()
		return
	}

	if err := database.RunMigrations(DB, "auth-service", migrations.FS); err != nil {
		slog.Error("Database migration failed", "error", err)
		os.Exit(1)
	}
	slog.Info("Database migration completed")
	seedPlans()
}

// legacyAutoMigrate is the pre-migrations GORM bootstrap, kept behind
// DB_AUTOMIGRATE for local development against a scratch database (and used by
// scripts/gen-migration-baseline.sh to regenerate the baseline). It is NOT the
// schema authority: changes must land as numbered migrations.
func legacyAutoMigrate() {
	if err := DB.AutoMigrate(
		&User{},
		&AuthMetadata{},
		&SubscriptionPlan{},
		&UserSession{},
		&PasswordResetToken{},
	); err != nil {
		slog.Error("Database migration failed", "error", err)
		os.Exit(1)
	}

	compositeIndexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_session_user_access_exp ON user_sessions (user_id, access_expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_session_access_refresh_exp ON user_sessions (access_expires_at) WHERE refresh_expires_at IS NOT NULL`,
	}
	for _, idx := range compositeIndexes {
		if err := DB.Exec(idx).Error; err != nil {
			slog.Warn("Failed to create composite index", "error", err)
		}
	}

	checkConstraints := []string{
		`DO $$ BEGIN ALTER TABLE users ADD CONSTRAINT chk_user_role CHECK (role IN ('user','admin','super-admin')); EXCEPTION WHEN duplicate_object THEN NULL; END $$`,
	}
	for _, chk := range checkConstraints {
		if err := DB.Exec(chk).Error; err != nil {
			slog.Warn("Failed to add check constraint", "error", err)
		}
	}

	slog.Info("Database migration completed")
}

// seedPlans upserts the canonical subscription plans so their limits are the
// single source of truth in the DB. Runs on every startup: it UPDATES existing
// rows (so changed limits propagate) and inserts any missing ones. The guest
// limits come from shared/config so the seeded DB row, the frontend (/auth/plans),
// and the gateway's guest fallback all agree.
func seedPlans() {
	// One-time rename of the legacy "anonymous" plan to "guest".
	if err := DB.Model(&SubscriptionPlan{}).
		Where("name = ?", "anonymous").
		Update("name", "guest").Error; err != nil {
		slog.Warn("Renaming legacy anonymous plan failed", "error", err)
	}

	plans := []SubscriptionPlan{
		{ID: uuid.New(), Name: "guest", MaxFileSizeMB: config.GuestMaxFileSizeMB(), MaxFilesPerJob: config.GuestMaxFilesPerJob(), RetentionDays: 0},
		{ID: uuid.New(), Name: "free", MaxFileSizeMB: 50, MaxFilesPerJob: 10, RetentionDays: 7},
		{ID: uuid.New(), Name: "pro", MaxFileSizeMB: 500, MaxFilesPerJob: 50, RetentionDays: 30},
	}
	// Upsert on the unique plan name: update the limit columns, keep the existing id.
	if err := DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"max_file_size_mb", "max_files_per_job", "retention_days"}),
	}).Create(&plans).Error; err != nil {
		slog.Warn("Plan seeding encountered an error", "error", err)
	} else {
		slog.Info("Subscription plans seeded")
	}
}

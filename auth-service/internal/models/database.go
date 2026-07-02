package models

import (
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"fyredocs/shared/database"
)

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

func Migrate() {
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
	seedPlans()
}

// seedPlans inserts the default subscription plans if they do not already exist.
// Uses INSERT ... ON CONFLICT DO NOTHING so it is safe to run on every startup.
func seedPlans() {
	plans := []SubscriptionPlan{
		{ID: uuid.New(), Name: "anonymous", MaxFileSizeMB: 10, MaxFilesPerJob: 5, RetentionDays: 0},
		{ID: uuid.New(), Name: "free", MaxFileSizeMB: 25, MaxFilesPerJob: 10, RetentionDays: 7},
		{ID: uuid.New(), Name: "pro", MaxFileSizeMB: 500, MaxFilesPerJob: 50, RetentionDays: 30},
	}
	if err := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&plans).Error; err != nil {
		slog.Warn("Plan seeding encountered an error", "error", err)
	} else {
		slog.Info("Subscription plans seeded")
	}
}

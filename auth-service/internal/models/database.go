package models

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var DB *gorm.DB

type PoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		MaxOpenConns:    25,
		MaxIdleConns:    10,
		ConnMaxLifetime: 30 * time.Minute,
	}
}

func Connect(pool ...PoolConfig) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		slog.Error("DATABASE_URL is not set")
		os.Exit(1)
	}

	cfg := DefaultPoolConfig()
	if len(pool) > 0 {
		if pool[0].MaxOpenConns > 0 {
			cfg.MaxOpenConns = pool[0].MaxOpenConns
		}
		if pool[0].MaxIdleConns > 0 {
			cfg.MaxIdleConns = pool[0].MaxIdleConns
		}
		if pool[0].ConnMaxLifetime > 0 {
			cfg.ConnMaxLifetime = pool[0].ConnMaxLifetime
		}
	}

	var err error
	DB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}

	sqlDB, err := DB.DB()
	if err != nil {
		slog.Error("Failed to create database handle", "error", err)
		os.Exit(1)
	}
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		slog.Error("Database ping failed", "error", err)
		os.Exit(1)
	}

	slog.Info("Database connection established")
}

func Migrate() {
	if err := DB.AutoMigrate(
		&User{},
		&AuthMetadata{},
		&SubscriptionPlan{},
		&UserSession{},
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

package models

import (
	"context"
	"log/slog"
	"os"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
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
		&ProcessingJob{},
		&FileMetadata{},
	); err != nil {
		slog.Error("Database migration failed", "error", err)
		os.Exit(1)
	}

	compositeIndexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_job_user_tool_created ON processing_jobs (user_id, tool_type, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_filemeta_job_kind ON file_metadata (job_id, kind)`,
	}
	for _, idx := range compositeIndexes {
		if err := DB.Exec(idx).Error; err != nil {
			slog.Warn("Failed to create composite index", "error", err)
		}
	}

	checkConstraints := []string{
		`DO $$ BEGIN ALTER TABLE processing_jobs ADD CONSTRAINT chk_job_status CHECK (status IN ('queued','processing','completed','failed')); EXCEPTION WHEN duplicate_object THEN NULL; END $$`,
	}
	for _, chk := range checkConstraints {
		if err := DB.Exec(chk).Error; err != nil {
			slog.Warn("Failed to add check constraint", "error", err)
		}
	}

	slog.Info("Database migration completed")
}

package models

import (
	"context"
	"log/slog"
	"os"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"fyredocs/shared/config"
)

var DB *gorm.DB

type PoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		MaxOpenConns:    15,
		MaxIdleConns:    5,
		ConnMaxLifetime: 5 * time.Minute,
		ConnMaxIdleTime: 2 * time.Minute,
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
	}

	dsn = config.ApplyPostgresDSNDefaults(dsn)

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
	sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
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
	if err := DB.AutoMigrate(&Document{}, &Folder{}, &Tag{}, &Export{}, &JobWorkspaceHint{}); err != nil {
		slog.Error("Database migration failed", "error", err)
		os.Exit(1)
	}

	// Full-text search: a generated tsvector column over name + extracted content,
	// plus a GIN index. Managed outside AutoMigrate so GORM doesn't fight the
	// generated column. Idempotent.
	stmts := []string{
		`ALTER TABLE documents ADD COLUMN IF NOT EXISTS search_vector tsvector
			GENERATED ALWAYS AS (to_tsvector('english', coalesce(name,'') || ' ' || coalesce(extracted_content,''))) STORED`,
		`CREATE INDEX IF NOT EXISTS idx_doc_search ON documents USING GIN (search_vector)`,
		`CREATE INDEX IF NOT EXISTS idx_doc_user_status_created ON documents (user_id, status, created_at DESC)`,
		// Idempotency for finalize: one document per (user, source job).
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_doc_user_source_job ON documents (user_id, source_job_id) WHERE source_job_id IS NOT NULL`,
		// Tag uniqueness is per scope: personal (user) vs organization. Drop the
		// old global (user_id, name) index in favour of two partial indexes.
		`DROP INDEX IF EXISTS idx_tag_user_name`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_tag_personal ON tags (user_id, name) WHERE organization_id IS NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_tag_org ON tags (organization_id, name) WHERE organization_id IS NOT NULL`,
	}
	for _, s := range stmts {
		if err := DB.Exec(s).Error; err != nil {
			slog.Warn("post-migration statement failed", "error", err)
		}
	}

	slog.Info("Database migration completed")
}

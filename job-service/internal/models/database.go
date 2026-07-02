package models

import (
	"log/slog"
	"os"
	"time"

	"gorm.io/gorm"

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

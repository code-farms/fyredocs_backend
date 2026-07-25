package models

import (
	"log/slog"
	"os"
	"time"

	"gorm.io/gorm"

	"job-service/internal/migrations"

	"fyredocs/shared/config"
	"fyredocs/shared/database"
)

// DB is the package-global GORM handle for job-service, initialized by Connect.
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

// Migrate brings the job-service schema up to date: it auto-migrates the job
// and file-metadata tables, adds composite indexes, and enforces the job-status
// check constraint. It fail-fasts on the core migration but treats indexes and
// constraints as best-effort.
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

	if err := database.RunMigrations(DB, "job-service", migrations.FS); err != nil {
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
		&ProcessingJob{},
		&FileMetadata{},
	); err != nil {
		slog.Error("Database migration failed", "error", err)
		os.Exit(1)
	}

	compositeIndexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_job_user_tool_created ON processing_jobs (user_id, tool_type, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_filemeta_job_kind ON file_metadata (job_id, kind)`,
		// GetJobHistory: user_id = ? ORDER BY created_at DESC (tool_type absent → the composite above can't serve the sort).
		`CREATE INDEX IF NOT EXISTS idx_job_user_created ON processing_jobs (user_id, created_at DESC)`,
		// cleanup uploadObjectConsumed: file_metadata WHERE path = ?
		`CREATE INDEX IF NOT EXISTS idx_filemeta_path ON file_metadata (path)`,
		// backfillExpiry sweep: UPDATE ... WHERE user_id IS NOT NULL AND expires_at IS NULL (runs every sweep).
		`CREATE INDEX IF NOT EXISTS idx_job_missing_expiry ON processing_jobs (id) WHERE expires_at IS NULL AND user_id IS NOT NULL`,
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

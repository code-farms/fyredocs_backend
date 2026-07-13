package models

import (
	"log/slog"
	"os"
	"time"

	"gorm.io/gorm"

	"fyredocs/shared/database"
)

// DB is the package-global GORM handle for document-service, initialized by Connect.
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

// Migrate brings the document-service schema up to date. Beyond auto-migrating
// the owned tables, it manages the full-text search column and the partial
// uniqueness indexes by hand (outside AutoMigrate) so GORM does not fight the
// generated column; all statements are idempotent and treated as best-effort.
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

package models

import (
	"time"

	"gorm.io/gorm"

	"fyredocs/shared/database"
)

// DB is the package-global GORM handle for convert-from-pdf, initialized by Connect.
var DB *gorm.DB

// PoolConfig aliases the shared pool settings so main() call sites keep
// their existing models.PoolConfig{...} overrides.
type PoolConfig = database.PoolConfig

// servicePoolBase is this service's default pool sizing; positive fields in a
// caller-supplied PoolConfig override it (see database.PoolConfig.WithOverrides).
func servicePoolBase() PoolConfig {
	return PoolConfig{
		MaxOpenConns:    5,
		MaxIdleConns:    2,
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

// Migrate auto-migrates this service's owned tables and fail-fasts on error.
// NOTE: This worker does NOT migrate processing_jobs / file_metadata. Those
// tables are owned and migrated solely by job-service (the schema owner) to
// avoid multiple services running concurrent, divergent AutoMigrate on the same
// tables. This worker only reads/updates ProcessingJob and writes its own
// FileMetadata output rows; the tables are guaranteed to exist because a worker
// only processes jobs that job-service (already migrated) has dispatched.

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
// fyredocs/shared/database. This worker does not migrate anything; see the
// note below on why schema ownership stays with job-service.
func Connect(pool ...PoolConfig) {
	DB = database.MustConnectFromEnv(servicePoolBase(), pool...)
}

// NOTE: There is deliberately no Migrate function in this package.
//
// processing_jobs and file_metadata are owned and migrated solely by
// job-service (the schema owner). If this worker also ran AutoMigrate, several
// services would race to alter the same tables with their own possibly
// divergent struct definitions. This worker only reads and updates
// ProcessingJob and writes its own FileMetadata output rows; the tables are
// guaranteed to already exist, because a worker only ever processes a job that
// job-service — which has migrated — dispatched to it.
//
// Do not "restore" a Migrate call here to make this package look like the
// request-serving services'; that would reintroduce the race.

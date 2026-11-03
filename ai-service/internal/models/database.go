// Package models holds ai-service's database handle and GORM models. The
// versioned SQL migrations in internal/migrations are the schema authority;
// the models mirror them for queries.
package models

import (
	"log/slog"
	"os"
	"time"

	"gorm.io/gorm"

	"ai-service/internal/migrations"

	"fyredocs/shared/config"
	"fyredocs/shared/database"
)

// DB is the package-global GORM handle for ai-service, initialized by Connect.
var DB *gorm.DB

// PoolConfig aliases the shared pool settings so main() call sites keep
// their existing models.PoolConfig{...} overrides.
type PoolConfig = database.PoolConfig

// Usage is one subject's token ledger row for one UTC month.
type Usage struct {
	Subject        string `gorm:"primaryKey"`
	Period         int    `gorm:"primaryKey"`
	InputTokens    int64  `gorm:"not null;default:0"`
	OutputTokens   int64  `gorm:"not null;default:0"`
	CachedTokens   int64  `gorm:"not null;default:0"`
	ReservedTokens int64  `gorm:"not null;default:0"`
	UpdatedAt      time.Time
}

// TableName pins the table to the migration's name.
func (Usage) TableName() string { return "ai_usage" }

// servicePoolBase is this service's default pool sizing; positive fields in a
// caller-supplied PoolConfig override it (see database.PoolConfig.WithOverrides).
// Small on purpose: an op holds a connection only for the quota read/write,
// never across the (long) model stream.
func servicePoolBase() PoolConfig {
	return PoolConfig{
		MaxOpenConns:    10,
		MaxIdleConns:    3,
		ConnMaxLifetime: 5 * time.Minute,
		ConnMaxIdleTime: 2 * time.Minute,
	}
}

// Connect opens the shared pooled GORM handle against DATABASE_URL and
// fail-fasts on any error.
func Connect(pool ...PoolConfig) {
	DB = database.MustConnectFromEnv(servicePoolBase(), pool...)
}

// Migrate applies the versioned migrations under a per-service advisory lock
// (see docs/developer/architecture/schema-migrations.md); a failure aborts
// startup, since a service that could not apply its schema must not serve.
func Migrate() {
	if config.GetEnvBool("DB_AUTOMIGRATE", false) {
		slog.Warn("DB_AUTOMIGRATE=true — using the legacy GORM AutoMigrate path instead of versioned migrations. Development only; never set this in production.")
		legacyAutoMigrate()
		return
	}
	if err := database.RunMigrations(DB, "ai-service", migrations.FS); err != nil {
		slog.Error("Database migration failed", "error", err)
		os.Exit(1)
	}
	slog.Info("Database migration completed")
}

// legacyAutoMigrate is the GORM bootstrap behind DB_AUTOMIGRATE, kept for
// parity with the other services and scripts/gen-migration-baseline.sh. It is
// NOT the schema authority: changes land as numbered migrations.
func legacyAutoMigrate() {
	if err := DB.AutoMigrate(&Usage{}); err != nil {
		slog.Error("Database migration failed", "error", err)
		os.Exit(1)
	}
	slog.Info("Database migration completed")
}

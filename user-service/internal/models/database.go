package models

import (
	"log/slog"
	"os"
	"time"

	"gorm.io/gorm"

	"fyredocs/shared/database"
)

// DB is the package-global GORM handle for user-service, initialized by Connect.
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

// Migrate brings the user-service schema up to date: it auto-migrates the owned
// tables and adds supporting indexes. It fail-fasts if the core migration fails
// but treats index creation as best-effort.
func Migrate() {
	if err := DB.AutoMigrate(&Organization{}, &Membership{}); err != nil {
		slog.Error("Database migration failed", "error", err)
		os.Exit(1)
	}
	stmts := []string{
		`CREATE INDEX IF NOT EXISTS idx_membership_user ON memberships (user_id)`,
	}
	for _, s := range stmts {
		if err := DB.Exec(s).Error; err != nil {
			slog.Warn("post-migration statement failed", "error", err)
		}
	}
	slog.Info("Database migration completed")
}

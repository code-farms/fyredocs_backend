// Package database holds the GORM/Postgres connection plumbing shared by
// every service: pool configuration, DSN defaults, open + ping. Schema
// ownership stays with each service — only the connection mechanics live
// here, so the ~70 lines of identical Connect() code aren't copied (and
// silently diverging) across the fleet.
//
// # Platform-wide data-access conventions
//
// Two properties of this backend surprise most readers on first contact. Both
// are deliberate, and both are recorded here because this is the one package
// every service's data layer goes through.
//
// No explicit transactions. There is not a single BeginTx/Commit/Rollback in
// the backend; every write is a single GORM statement, which Postgres already
// executes atomically. Multi-step operations instead achieve safety by being
// idempotent and replayable — job creation, for instance, guards against
// double-spend with Redis upload claims and idempotency records rather than by
// wrapping writes in a transaction (see job-service/handlers/jobs.go). This
// matters because "these two writes should be in a transaction" is the obvious
// reviewer instinct, and acting on it would add lock contention and a new
// failure mode without removing the retry-safety requirement that the
// idempotency scheme exists to satisfy. If a genuine multi-row invariant ever
// appears, adding a transaction is correct — just do it knowingly.
//
// No buffer pooling. There is no sync.Pool anywhere, and that is a consequence
// of a stronger rule: user file bytes are never accumulated in memory. Uploads
// stream to object storage, downloads stream back, content sniffing reads only
// the leading 512 bytes, and the api-gateway's response wrapper deliberately
// avoids buffering bodies so SSE and downloads keep flowing. Since no
// large-buffer hot path exists, there is nothing to pool. Reintroducing
// whole-file buffering to enable pooling would trade a bounded memory profile
// for an unbounded one.
//
// # Hardening finding IDs
//
// Comments across the backend cite finding IDs such as (finding B1) or
// (finding I1). Those resolve to docs/developer/architecture/backend-hardening.md
// section 3.20, which is the index of the hardening audit and the rationale for
// the guard the comment sits on. A separate audit,
// docs/developer/audit/production-readiness-2026-07.md, uses an unrelated
// C1-C6/H1-H10 scheme — the two numbering systems must not be conflated.
package database

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"fyredocs/shared/config"
)

// PoolConfig holds sql.DB connection-pool settings.
type PoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// DefaultPoolConfig is tuned for managed Postgres pools (Neon, RDS Proxy,
// pgbouncer) that close idle TCP connections server-side. ConnMaxIdleTime is
// shorter than typical server idle-close windows so the local pool never holds
// a half-dead socket.
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		MaxOpenConns:    25,
		MaxIdleConns:    10,
		ConnMaxLifetime: 5 * time.Minute,
		ConnMaxIdleTime: 2 * time.Minute,
	}
}

// WithOverrides returns base with every positive field of o applied on top.
// Zero-valued fields in o leave the base value untouched, preserving the
// partial-override semantics services have always exposed to their main().
func (base PoolConfig) WithOverrides(o PoolConfig) PoolConfig {
	if o.MaxOpenConns > 0 {
		base.MaxOpenConns = o.MaxOpenConns
	}
	if o.MaxIdleConns > 0 {
		base.MaxIdleConns = o.MaxIdleConns
	}
	if o.ConnMaxLifetime > 0 {
		base.ConnMaxLifetime = o.ConnMaxLifetime
	}
	if o.ConnMaxIdleTime > 0 {
		base.ConnMaxIdleTime = o.ConnMaxIdleTime
	}
	return base
}

// ConnectFromEnv opens a pooled GORM handle against DATABASE_URL and verifies
// it with a 5s ping. base carries the service's pool sizing; any positive
// fields in overrides win over base.
func ConnectFromEnv(base PoolConfig, overrides ...PoolConfig) (*gorm.DB, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, fmt.Errorf("DATABASE_URL is not set")
	}

	cfg := base
	if len(overrides) > 0 {
		cfg = base.WithOverrides(overrides[0])
	}

	dsn = config.ApplyPostgresDSNDefaults(dsn)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("database handle: %w", err)
	}
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)

	// Expose pool stats on /metrics so Prometheus can alert on pool exhaustion.
	registerPoolMetrics(sqlDB)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("database ping: %w", err)
	}

	return db, nil
}

// MustConnectFromEnv is ConnectFromEnv with the fail-fast behavior every
// service main() wants: log and exit on any failure.
func MustConnectFromEnv(base PoolConfig, overrides ...PoolConfig) *gorm.DB {
	db, err := ConnectFromEnv(base, overrides...)
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	slog.Info("Database connection established")
	return db
}

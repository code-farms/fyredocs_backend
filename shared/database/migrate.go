package database

import (
	"context"
	"database/sql"
	"fmt"
	"hash/fnv"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
	"github.com/pressly/goose/v3/lock"
	"gorm.io/gorm"
)

// RunMigrations applies a service's embedded SQL migrations. Each service owns
// a disjoint set of tables in the shared database, so each gets its own goose
// history table (goose_db_version_<service>) — schema ownership stays with the
// service that owns the tables.
//
// Migrations run under a Postgres session advisory lock, so N replicas booting
// at once (docker compose --scale) serialize instead of racing the same DDL.
// The lock is per-service (derived from the service name), so services don't
// block each other.
//
// Because session advisory locks are incompatible with transaction-mode
// connection pooling, migrations connect through DATABASE_URL_DIRECT when it is
// set (bypassing PgBouncer), falling back to DATABASE_URL.
//
// Failures are returned, not warned about: a service that could not apply its
// schema must not start serving.
func RunMigrations(gormDB *gorm.DB, service string, fsys fs.FS) error {
	db, cleanup, err := migrationDB(gormDB)
	if err != nil {
		return err
	}
	defer cleanup()

	locker, err := lock.NewPostgresSessionLocker(
		lock.WithLockID(advisoryLockID(service)),
		lock.WithLockTimeout(5, 60),   // probe every 5s, give up after ~5m
		lock.WithUnlockTimeout(5, 12), // probe every 5s, give up after ~1m
	)
	if err != nil {
		return fmt.Errorf("migrations: build advisory locker: %w", err)
	}

	// DialectCustom + WithStore: the store carries both the dialect and the
	// per-service history table name. Passing a named dialect alongside a
	// custom store is rejected by goose.
	provider, err := goose.NewProvider(goose.DialectCustom, db, fsys,
		goose.WithStore(mustStore(service)),
		goose.WithSessionLocker(locker),
	)
	if err != nil {
		return fmt.Errorf("migrations: build provider: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), migrationTimeout())
	defer cancel()

	// Clear the query timeout for this session: the 15s guard that protects
	// request-path queries (set as a role-in-database default so it survives
	// PgBouncer) would abort a legitimate index build. The migration run is
	// bounded by ctx instead.
	if _, err := db.ExecContext(ctx, "SET statement_timeout = 0"); err != nil {
		return fmt.Errorf("migrations: clear statement_timeout: %w", err)
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("migrations: apply: %w", err)
	}
	if len(results) == 0 {
		slog.Info("database schema already up to date", "service", service)
		return nil
	}
	for _, r := range results {
		slog.Info("applied migration", "service", service, "version", r.Source.Version,
			"name", r.Source.Path, "duration", r.Duration.String())
	}
	return nil
}

// mustStore builds the per-service goose store (history table). goose validates
// the table name, and ours is derived from a service name we control, so an
// error here is a programming mistake — surface it at the call site instead of
// panicking at boot.
func mustStore(service string) database.Store {
	store, err := database.NewStore(database.DialectPostgres, historyTable(service))
	if err != nil {
		// NewStore only rejects an empty table name; historyTable never returns one.
		panic(fmt.Sprintf("migrations: invalid history table for %q: %v", service, err))
	}
	return store
}

// historyTable is the per-service goose version table name.
func historyTable(service string) string {
	return "goose_db_version_" + strings.ReplaceAll(strings.TrimSpace(service), "-", "_")
}

// advisoryLockID derives a stable per-service lock id so concurrent replicas of
// the SAME service serialize, while different services migrate in parallel.
func advisoryLockID(service string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("fyredocs-migrations:" + service))
	// Mask the sign bit: pg_advisory_lock takes a signed bigint and a negative
	// id is legal but confusing in pg_locks output.
	return int64(h.Sum64() & 0x7FFFFFFFFFFFFFFF)
}

// migrationTimeout bounds a migration run. Generous by default because an
// index build on a large table legitimately takes minutes.
func migrationTimeout() time.Duration {
	if v := strings.TrimSpace(os.Getenv("DB_MIGRATION_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 10 * time.Minute
}

// migrationDB returns the *sql.DB migrations should run on. When
// DATABASE_URL_DIRECT is set it opens a dedicated connection to that DSN (used
// to bypass PgBouncer, whose transaction pooling breaks session advisory
// locks); otherwise it reuses the service's existing GORM pool. The returned
// cleanup closes the dedicated connection, if one was opened.
func migrationDB(gormDB *gorm.DB) (*sql.DB, func(), error) {
	if direct := strings.TrimSpace(os.Getenv("DATABASE_URL_DIRECT")); direct != "" {
		db, err := sql.Open("pgx", direct)
		if err != nil {
			return nil, nil, fmt.Errorf("migrations: open DATABASE_URL_DIRECT: %w", err)
		}
		// One connection only: the advisory lock is session-scoped, so every
		// migration statement must run on the same backend.
		db.SetMaxOpenConns(1)
		return db, func() { _ = db.Close() }, nil
	}

	if gormDB == nil {
		return nil, nil, fmt.Errorf("migrations: no database handle")
	}
	sqlDB, err := gormDB.DB()
	if err != nil {
		return nil, nil, fmt.Errorf("migrations: unwrap sql.DB: %w", err)
	}
	return sqlDB, func() {}, nil
}

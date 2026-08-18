//go:build integration

// Package dbtest is the shared harness for integration-tagged database tests.
// It connects every service's test suite to one dedicated *_test database on
// the essentials stack (deployment/docker-compose.essentials.yml), creates
// that database on first use, and applies the service's own embedded goose
// migrations through database.RunMigrations — the same code path production
// boots through, so a migration that only works via GORM AutoMigrate cannot
// hide here.
//
// Isolation contract (documented in docs/developer/architecture/
// testing-strategy.md): services own disjoint table sets, so suites from
// different services may share the database but must run with -p 1 (the
// Makefile's test-integration target enforces it). Within a suite, prefer
// Tx for per-test isolation; fall back to Truncate for code under test that
// commits, spawns goroutines holding the handle, or must observe rows from
// another connection (ON CONFLICT dedupe, SetNX claims, cleanup sweeps).
package dbtest

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"io/fs"

	"fyredocs/shared/database"
)

// defaultTestURL builds the DSN for the essentials stack's loopback
// publishing. The db container takes its credentials from the root .env
// (POSTGRES_USER/POSTGRES_PASSWORD), so the default must too — the Makefile's
// test-integration target sources .env with set -a, the same way deploy.sh
// does. FYREDOCS_TEST_DATABASE_URL overrides the whole DSN when the stack
// runs elsewhere.
func defaultTestURL() string {
	user := envOr("POSTGRES_USER", "fyredocs")
	pass := envOr("POSTGRES_PASSWORD", "fyredocs")
	name := envOr("POSTGRES_DB", "fyredocs") + "_test"
	host := envOr("FYREDOCS_TEST_DB_HOST", "localhost:5432")
	return fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
		url.QueryEscape(user), url.QueryEscape(pass), host, name)
}

// envOr trims the variable and falls back when it is unset or blank.
func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// identifier restricts database names to what ensureDatabase can safely embed
// in CREATE DATABASE — the statement cannot be parameterized.
var identifier = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// Open connects to the test database, creating it if missing, and applies the
// named service's migrations (nil skips migration — for harness self-tests).
// The database name must end in _test: everything else about this harness
// (TRUNCATE helpers, throwaway rows) is destructive, and the suffix check is
// what stands between a mispasted DATABASE_URL and the real data.
func Open(t *testing.T, service string, migrations fs.FS) *gorm.DB {
	t.Helper()

	dsn := strings.TrimSpace(os.Getenv("FYREDOCS_TEST_DATABASE_URL"))
	if dsn == "" {
		dsn = defaultTestURL()
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("dbtest: parse FYREDOCS_TEST_DATABASE_URL: %v", err)
	}
	dbName := strings.TrimPrefix(u.Path, "/")
	if !strings.HasSuffix(dbName, "_test") {
		t.Fatalf("dbtest: refusing to run against %q — the test database name must end in _test", dbName)
	}
	if !identifier.MatchString(dbName) {
		t.Fatalf("dbtest: unsafe database name %q", dbName)
	}

	ensureDatabase(t, u, dbName)

	// Migrations must run on the handle opened below: DATABASE_URL_DIRECT
	// would silently redirect them to whatever server that variable names
	// (see database.migrationDB). t.Setenv doubles as the serial-test guard.
	t.Setenv("DATABASE_URL_DIRECT", "")

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("dbtest: connect %s: %v", redacted(u), err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	if migrations != nil {
		if err := database.RunMigrations(db, service, migrations); err != nil {
			t.Fatalf("dbtest: migrate %s: %v", service, err)
		}
	}
	return db
}

// Tx returns a transaction that rolls back in Cleanup — callers swap it into
// their service's package-global models.DB for per-test isolation. Unusable
// when the code under test commits, holds the handle in goroutines, or must
// see rows from another connection; use Truncate cleanup for those.
func Tx(t testing.TB, db *gorm.DB) *gorm.DB {
	t.Helper()
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatalf("dbtest: begin tx: %v", tx.Error)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

// Truncate empties the listed tables now and again in Cleanup, so a test both
// starts clean after an earlier crash and leaves nothing for the next one.
// RESTART IDENTITY CASCADE because leftover sequence values and FK rows are
// exactly the cross-test contamination this exists to stop.
func Truncate(t testing.TB, db *gorm.DB, tables ...string) {
	t.Helper()
	if len(tables) == 0 {
		return
	}
	quoted := make([]string, len(tables))
	for i, tbl := range tables {
		if !identifier.MatchString(tbl) {
			t.Fatalf("dbtest: unsafe table name %q", tbl)
		}
		quoted[i] = `"` + tbl + `"`
	}
	stmt := "TRUNCATE TABLE " + strings.Join(quoted, ", ") + " RESTART IDENTITY CASCADE"
	run := func() {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("dbtest: %s: %v", stmt, err)
		}
	}
	run()
	t.Cleanup(run)
}

// ensureDatabase creates dbName via the server's maintenance database. A
// concurrent creator is fine: duplicate_database (SQLSTATE 42P04) is success.
func ensureDatabase(t *testing.T, u *url.URL, dbName string) {
	t.Helper()

	admin := *u
	admin.Path = "/postgres"
	adminDB, err := gorm.Open(postgres.Open(admin.String()), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("dbtest: connect maintenance db %s (is the essentials stack up? make test-infra-up): %v", redacted(&admin), err)
	}
	defer func() {
		if sqlDB, err := adminDB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}()

	err = adminDB.Exec(fmt.Sprintf(`CREATE DATABASE %q`, dbName)).Error
	if err != nil && !strings.Contains(err.Error(), "42P04") &&
		!strings.Contains(err.Error(), "already exists") {
		t.Fatalf("dbtest: create %s: %v", dbName, err)
	}
}

// redacted renders a DSN for error messages with the password removed.
func redacted(u *url.URL) string {
	c := *u
	if c.User != nil {
		c.User = url.User(c.User.Username())
	}
	return c.String()
}

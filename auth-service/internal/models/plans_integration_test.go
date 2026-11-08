//go:build integration

package models

import (
	"strings"
	"testing"

	"gorm.io/gorm"

	"auth-service/internal/migrations"
	"fyredocs/shared/database/dbtest"
)

// withPlansDB points the package handle at the migrated test database with
// an empty subscription_plans table.
func withPlansDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := dbtest.Open(t, "auth-service", migrations.FS)
	prev := DB
	DB = db
	t.Cleanup(func() { DB = prev })
	dbtest.Truncate(t, db, "users", "user_sessions", "subscription_plans")
	return db
}

func planRow(t *testing.T, db *gorm.DB, name string) SubscriptionPlan {
	t.Helper()
	var p SubscriptionPlan
	if err := db.Where("name = ?", name).First(&p).Error; err != nil {
		t.Fatalf("load plan %s: %v", name, err)
	}
	return p
}

// TestSeedPlans_DB_InsertOnly pins that a fresh database gets the canonical
// rows and that a restart never overwrites an operator's edit — the property
// that makes the table, not this file, the source of plan limits.
func TestSeedPlans_DB_InsertOnly(t *testing.T) {
	db := withPlansDB(t)
	seedPlans()

	want := map[string][3]int{"guest": {20, 5, 100}, "free": {50, 10, 300}, "pro": {300, 50, 300}}
	for name, w := range want {
		p := planRow(t, db, name)
		if got := [3]int{p.MaxFileSizeMB, p.MaxFilesPerJob, p.MaxJobInputMB}; got != w {
			t.Errorf("%s = %v, want %v", name, got, w)
		}
	}

	if err := db.Model(&SubscriptionPlan{}).Where("name = ?", "pro").Update("max_file_size_mb", 250).Error; err != nil {
		t.Fatal(err)
	}
	seedPlans() // a restart
	if got := planRow(t, db, "pro").MaxFileSizeMB; got != 250 {
		t.Errorf("pro max_file_size_mb after reseed = %d, want the edited 250", got)
	}
	var n int64
	db.Model(&SubscriptionPlan{}).Count(&n)
	if n != 3 {
		t.Errorf("plan rows = %d, want 3 (no duplicates)", n)
	}
}

// TestSeedPlans_DB_RenamesAnonymous pins the one-time legacy rename.
func TestSeedPlans_DB_RenamesAnonymous(t *testing.T) {
	db := withPlansDB(t)
	legacy := SubscriptionPlan{Name: "anonymous", MaxFileSizeMB: 20, MaxFilesPerJob: 5, MaxJobInputMB: 100}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	seedPlans()
	var n int64
	db.Model(&SubscriptionPlan{}).Where("name = ?", "anonymous").Count(&n)
	if n != 0 {
		t.Error("anonymous row still present")
	}
	if got := planRow(t, db, "guest").ID; got != legacy.ID {
		t.Errorf("guest row id = %s, want the renamed legacy row %s", got, legacy.ID)
	}
}

// TestMigration00002_DB_DownUp runs the migration's Down then Up SQL inside a
// transaction that is rolled back (Postgres DDL is transactional), so the
// shared test schema is untouched. It pins both directions: Down restores pro
// to 500 MB and drops the column; Up re-adds it with the 300 default and sets
// pro 300 and guest 100.
func TestMigration00002_DB_DownUp(t *testing.T) {
	db := withPlansDB(t)
	body, err := migrations.FS.ReadFile("00002_plan_job_input_limit.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(body), "-- +goose Down", 2)
	if len(parts) != 2 {
		t.Fatal("migration has no Down section")
	}
	up, down := parts[0][strings.Index(parts[0], "-- +goose Up"):], parts[1]

	tx := db.Begin()
	t.Cleanup(func() { tx.Rollback() })
	for _, name := range []string{"guest", "free", "pro"} {
		if err := tx.Exec(`INSERT INTO subscription_plans (id, name, max_file_size_mb, max_files_per_job, retention_days)
			VALUES (gen_random_uuid(), ?, 300, 5, 0)`, name).Error; err != nil {
			t.Fatal(err)
		}
	}
	hasColumn := func() bool {
		var n int64
		tx.Raw(`SELECT count(*) FROM information_schema.columns
			WHERE table_name = 'subscription_plans' AND column_name = 'max_job_input_mb'`).Scan(&n)
		return n == 1
	}

	if err := tx.Exec(down).Error; err != nil {
		t.Fatalf("down: %v", err)
	}
	if hasColumn() {
		t.Error("down left max_job_input_mb in place")
	}
	var proMB int
	tx.Raw(`SELECT max_file_size_mb FROM subscription_plans WHERE name = 'pro'`).Scan(&proMB)
	if proMB != 500 {
		t.Errorf("down: pro max_file_size_mb = %d, want 500", proMB)
	}

	if err := tx.Exec(up).Error; err != nil {
		t.Fatalf("up: %v", err)
	}
	if !hasColumn() {
		t.Fatal("up did not add max_job_input_mb")
	}
	got := map[string][2]int{}
	rows, _ := tx.Raw(`SELECT name, max_file_size_mb, max_job_input_mb FROM subscription_plans`).Rows()
	for rows.Next() {
		var name string
		var file, job int
		_ = rows.Scan(&name, &file, &job)
		got[name] = [2]int{file, job}
	}
	_ = rows.Close()
	want := map[string][2]int{"guest": {300, 100}, "free": {300, 300}, "pro": {300, 300}}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("up: %s (file, job) = %v, want %v", name, got[name], w)
		}
	}
}

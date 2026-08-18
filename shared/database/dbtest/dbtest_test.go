//go:build integration

// Self-test for the harness itself: proves Open reaches the *_test database,
// Tx isolation rolls back, and Truncate leaves tables empty — so a service
// suite failing on top of dbtest can rule the harness out.
package dbtest

import "testing"

func TestOpenTxAndTruncate(t *testing.T) {
	db := Open(t, "dbtest-self", nil)

	if err := db.Exec(`CREATE TABLE IF NOT EXISTS dbtest_self (id serial PRIMARY KEY, note text)`).Error; err != nil {
		t.Fatalf("create scratch table: %v", err)
	}
	t.Cleanup(func() { _ = db.Exec(`DROP TABLE IF EXISTS dbtest_self`).Error })

	t.Run("tx rolls back", func(t *testing.T) {
		tx := Tx(t, db)
		if err := tx.Exec(`INSERT INTO dbtest_self (note) VALUES ('tx-row')`).Error; err != nil {
			t.Fatalf("insert in tx: %v", err)
		}
		var n int64
		if err := tx.Raw(`SELECT count(*) FROM dbtest_self`).Scan(&n).Error; err != nil || n != 1 {
			t.Fatalf("tx sees %d rows (err %v), want 1", n, err)
		}
	})

	var after int64
	if err := db.Raw(`SELECT count(*) FROM dbtest_self`).Scan(&after).Error; err != nil || after != 0 {
		t.Fatalf("rolled-back row leaked: count = %d (err %v)", after, err)
	}

	t.Run("truncate empties committed rows", func(t *testing.T) {
		if err := db.Exec(`INSERT INTO dbtest_self (note) VALUES ('committed')`).Error; err != nil {
			t.Fatalf("insert: %v", err)
		}
		Truncate(t, db, "dbtest_self")
		var n int64
		if err := db.Raw(`SELECT count(*) FROM dbtest_self`).Scan(&n).Error; err != nil || n != 0 {
			t.Fatalf("count after truncate = %d (err %v), want 0", n, err)
		}
	})
}

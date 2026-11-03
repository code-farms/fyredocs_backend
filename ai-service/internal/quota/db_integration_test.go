//go:build integration

// Integration tests for the ledger's atomicity: the reserve check and
// increment are one statement, so concurrent requests cannot jointly overrun
// a limit. Uses Truncate isolation because the reservations commit and must
// be visible across connections.
package quota

import (
	"sync"
	"testing"
	"time"

	"fyredocs/shared/database/dbtest"

	"ai-service/internal/llm"
	"ai-service/internal/migrations"
)

func ledger(t *testing.T, limit int64) *Ledger {
	db := dbtest.Open(t, "ai-service", migrations.FS)
	dbtest.Truncate(t, db, "ai_usage")
	fixed := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	return &Ledger{DB: db, Limits: Limits{Free: limit, Pro: limit * 10}, Now: func() time.Time { return fixed }}
}

func TestConcurrentReservationsNeverOverrun(t *testing.T) {
	l := ledger(t, 1000)
	var wg sync.WaitGroup
	var mu sync.Mutex
	granted := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := l.Reserve(t.Context(), "u1", "free", 100); err == nil {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if granted != 10 {
		t.Fatalf("granted %d reservations of 100 against a 1000 limit, want exactly 10", granted)
	}
}

func TestSettleReleasesHoldAndRecordsUsage(t *testing.T) {
	l := ledger(t, 1000)
	r, err := l.Reserve(t.Context(), "u2", "free", 600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Reserve(t.Context(), "u2", "free", 600); err != ErrExceeded {
		t.Fatalf("second hold should not fit: %v", err)
	}
	if err := l.Settle(t.Context(), r, llm.Usage{InputTokens: 150, OutputTokens: 50, CachedTokens: 100}); err != nil {
		t.Fatal(err)
	}
	st, err := l.Status(t.Context(), "u2", "free")
	if err != nil {
		t.Fatal(err)
	}
	if st.UsedTokens != 200 || st.ReservedTokens != 0 || st.RemainingTokens != 800 || st.Period != "2026-11" {
		t.Fatalf("%+v", st)
	}
	if _, err := l.Reserve(t.Context(), "u2", "free", 600); err != nil {
		t.Fatalf("hold should fit after settle: %v", err)
	}
}

func TestSubjectsAreIsolated(t *testing.T) {
	l := ledger(t, 100)
	if _, err := l.Reserve(t.Context(), "a", "free", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Reserve(t.Context(), "b", "free", 100); err != nil {
		t.Fatalf("user b must not be charged for user a: %v", err)
	}
}

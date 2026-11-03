package quota

import (
	"testing"
	"time"
)

func TestLimitsFor(t *testing.T) {
	l := Limits{Free: 10, Pro: 100}
	for plan, want := range map[string]int64{"pro": 100, " PRO ": 100, "free": 10, "": 10, "enterprise": 10} {
		if got := l.For(plan); got != want {
			t.Errorf("For(%q)=%d, want %d", plan, got, want)
		}
	}
}

func TestPeriodIsUTCMonth(t *testing.T) {
	ist := time.FixedZone("IST", 5*3600+1800)
	// 1 Dec 03:00 IST is still 30 Nov in UTC.
	if got := Period(time.Date(2026, 12, 1, 3, 0, 0, 0, ist)); got != 202611 {
		t.Fatalf("got %d", got)
	}
	if periodLabel(202611) != "2026-11" {
		t.Fatal(periodLabel(202611))
	}
	if !resetsAt(202612).Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal(resetsAt(202612))
	}
}

func TestLimitsFromEnv(t *testing.T) {
	t.Setenv("AI_QUOTA_FREE_TOKENS", "5")
	t.Setenv("AI_QUOTA_PRO_TOKENS", "0")
	if l := LimitsFromEnv(); l.Free != 5 || l.Pro != 0 {
		t.Fatalf("%+v", l)
	}
}

func TestReserveOverLimitNeverTouchesDB(t *testing.T) {
	// DB is nil: an over-limit request must be refused before any query.
	l := &Ledger{Limits: Limits{Free: 10}}
	if _, err := l.Reserve(t.Context(), "u", "free", 11); err != ErrExceeded {
		t.Fatalf("got %v", err)
	}
}

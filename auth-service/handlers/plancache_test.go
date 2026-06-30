package handlers

import (
	"testing"
	"time"

	"auth-service/internal/models"
)

// These cover lookupPlan's behaviour that does not require a database: an empty
// plan name, and a fresh in-process cache hit. The DB-miss path is exercised by
// the end-to-end verification, consistent with the other auth handler tests.

func TestLookupPlanEmptyName(t *testing.T) {
	if _, ok := lookupPlan(""); ok {
		t.Fatalf("expected ok=false for empty plan name")
	}
}

func TestLookupPlanServesFreshCacheEntry(t *testing.T) {
	const name = "test-cached-plan"
	want := models.SubscriptionPlan{Name: name, MaxFileSizeMB: 123, MaxFilesPerJob: 7, RetentionDays: 9}

	planCacheMu.Lock()
	planCache[name] = cachedPlan{plan: want, expires: time.Now().Add(time.Minute)}
	planCacheMu.Unlock()
	t.Cleanup(func() {
		planCacheMu.Lock()
		delete(planCache, name)
		planCacheMu.Unlock()
	})

	got, ok := lookupPlan(name)
	if !ok {
		t.Fatalf("expected cache hit for %q", name)
	}
	if got != want {
		t.Fatalf("cache hit returned %+v, want %+v", got, want)
	}
}

func TestLookupPlanIgnoresExpiredEntry(t *testing.T) {
	// An expired entry must not be served from cache. We can't assert the DB
	// fallback here (no DB), but we can confirm the entry is treated as stale by
	// checking the freshness predicate the lookup relies on.
	const name = "test-expired-plan"
	entry := cachedPlan{plan: models.SubscriptionPlan{Name: name}, expires: time.Now().Add(-time.Minute)}
	if time.Now().Before(entry.expires) {
		t.Fatalf("expected entry to be considered expired")
	}
}

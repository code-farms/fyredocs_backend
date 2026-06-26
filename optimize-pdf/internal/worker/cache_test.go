package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// testLogger returns a logger that discards output, keeping test runs quiet.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeCache is a map-backed cacheStore for tests.
type fakeCache struct {
	mu   sync.Mutex
	data map[string]string
}

func newFakeCache() *fakeCache { return &fakeCache{data: map[string]string{}} }

func (c *fakeCache) get(ctx context.Context, key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.data[key]
	return v, ok
}

func (c *fakeCache) set(ctx context.Context, key, val string, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = val
}

func TestBuildCacheKeyDeterministic(t *testing.T) {
	opts := json.RawMessage(`{"quality":"high"}`)
	k1 := buildCacheKey("word-to-pdf", opts, []string{"a", "b"})
	k2 := buildCacheKey("word-to-pdf", opts, []string{"a", "b"})
	if k1 != k2 {
		t.Errorf("identical inputs must yield identical keys: %q vs %q", k1, k2)
	}
}

func TestBuildCacheKeyETagOrderIndependent(t *testing.T) {
	opts := json.RawMessage(`{}`)
	k1 := buildCacheKey("merge-pdf", opts, []string{"a", "b", "c"})
	k2 := buildCacheKey("merge-pdf", opts, []string{"c", "a", "b"})
	if k1 != k2 {
		t.Errorf("ETag order must not affect key: %q vs %q", k1, k2)
	}
}

func TestBuildCacheKeySensitivity(t *testing.T) {
	base := buildCacheKey("word-to-pdf", json.RawMessage(`{"q":"high"}`), []string{"a"})
	cases := map[string]string{
		"different tool":    buildCacheKey("excel-to-pdf", json.RawMessage(`{"q":"high"}`), []string{"a"}),
		"different options": buildCacheKey("word-to-pdf", json.RawMessage(`{"q":"low"}`), []string{"a"}),
		"different etag":    buildCacheKey("word-to-pdf", json.RawMessage(`{"q":"high"}`), []string{"b"}),
	}
	for name, k := range cases {
		if k == base {
			t.Errorf("%s should change the cache key but did not", name)
		}
	}
	if base[:len(cacheKeyPrefix)] != cacheKeyPrefix {
		t.Errorf("key missing prefix %q: %q", cacheKeyPrefix, base)
	}
}

func TestCanonicalOptions(t *testing.T) {
	// Key order must not matter.
	a := canonicalOptions(json.RawMessage(`{"a":1,"b":2}`))
	b := canonicalOptions(json.RawMessage(`{"b":2,"a":1}`))
	if string(a) != string(b) {
		t.Errorf("canonicalOptions must normalise key order: %q vs %q", a, b)
	}
	// Empty options → "{}".
	if got := string(canonicalOptions(nil)); got != "{}" {
		t.Errorf("empty options = %q, want {}", got)
	}
	// Invalid JSON falls back to raw bytes.
	raw := json.RawMessage(`not json`)
	if got := string(canonicalOptions(raw)); got != "not json" {
		t.Errorf("invalid JSON = %q, want raw passthrough", got)
	}
}

func TestCacheTTL(t *testing.T) {
	t.Setenv("RESULT_CACHE_TTL_SECONDS", "")
	if got := cacheTTL(); got != time.Hour {
		t.Errorf("default TTL = %v, want 1h", got)
	}
	t.Setenv("RESULT_CACHE_TTL_SECONDS", "120")
	if got := cacheTTL(); got != 120*time.Second {
		t.Errorf("custom TTL = %v, want 120s", got)
	}
	t.Setenv("RESULT_CACHE_TTL_SECONDS", "0")
	if got := cacheTTL(); got != 0 {
		t.Errorf("disabled TTL = %v, want 0", got)
	}
}

func TestNewCacheStoreNil(t *testing.T) {
	if newCacheStore(nil) != nil {
		t.Error("newCacheStore(nil) must be nil so caching is disabled")
	}
}

func TestInputETags(t *testing.T) {
	store := newFakeStorage()
	store.etagByKey["test-uploads/k1"] = "e1"
	store.etagByKey["test-uploads/k2"] = "e2"

	etags, err := inputETags(context.Background(), store, []string{"k1", "k2"})
	if err != nil {
		t.Fatalf("inputETags error: %v", err)
	}
	if len(etags) != 2 || etags[0] != "e1" || etags[1] != "e2" {
		t.Errorf("etags = %v, want [e1 e2]", etags)
	}
}

func TestInputETagsStatError(t *testing.T) {
	store := newFakeStorage()
	store.statObjectErr = errors.New("network down")
	if _, err := inputETags(context.Background(), store, []string{"k1"}); err == nil {
		t.Fatal("expected error when StatObject fails")
	}
}

func TestTryServeFromCacheMiss(t *testing.T) {
	cache := newFakeCache()
	cfg := WorkerConfig{Storage: newFakeStorage()}
	payload := JobPayload{JobID: uuid.NewString(), ToolType: "word-to-pdf"}
	jobID, _ := uuid.Parse(payload.JobID)
	if tryServeFromCache(context.Background(), cfg, cache, "missing-key", payload, jobID, testLogger()) {
		t.Error("expected false on cache miss")
	}
}

func TestTryServeFromCacheInvalidJSON(t *testing.T) {
	cache := newFakeCache()
	cache.set(context.Background(), "k", "not-json", time.Minute)
	cfg := WorkerConfig{Storage: newFakeStorage()}
	payload := JobPayload{JobID: uuid.NewString(), ToolType: "word-to-pdf"}
	jobID, _ := uuid.Parse(payload.JobID)
	if tryServeFromCache(context.Background(), cfg, cache, "k", payload, jobID, testLogger()) {
		t.Error("expected false on invalid cached JSON")
	}
}

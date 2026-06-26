package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Result caching deduplicates identical conversions. The cache key is derived
// from the tool type, the (canonicalised) options, and the content identity of
// every input — the latter via each upload object's ETag, fetched with a cheap
// StatObject (no download). On a hit, the previously produced output object is
// server-side copied to the new job's output key, skipping the download and the
// conversion subprocess entirely. Cache entries are best-effort: any error in
// the cache path is logged and the job proceeds normally, so caching can never
// fail a conversion.

const cacheKeyPrefix = "rescache:v1:organize-pdf:"

// cachedResult is the value stored in Redis for a completed conversion.
type cachedResult struct {
	OutputKey string                 `json:"outputKey"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// cacheStore is the narrow Redis surface the cache needs, so tests can supply a
// map-backed fake. Implementations are best-effort and never return errors.
type cacheStore interface {
	get(ctx context.Context, key string) (string, bool)
	set(ctx context.Context, key, val string, ttl time.Duration)
}

// redisCacheStore adapts *redis.Client to cacheStore, swallowing (and logging)
// errors so the cache is always best-effort.
type redisCacheStore struct{ rdb *redis.Client }

func (r redisCacheStore) get(ctx context.Context, key string) (string, bool) {
	v, err := r.rdb.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", false
	}
	if err != nil {
		slog.Warn("result cache get failed", "error", err)
		return "", false
	}
	return v, true
}

func (r redisCacheStore) set(ctx context.Context, key, val string, ttl time.Duration) {
	if err := r.rdb.Set(ctx, key, val, ttl).Err(); err != nil {
		slog.Warn("result cache set failed", "error", err)
	}
}

// newCacheStore returns a cacheStore for rdb, or nil if rdb is nil (caching
// disabled). Kept separate so the wiring stays a one-liner in worker.go.
func newCacheStore(rdb *redis.Client) cacheStore {
	if rdb == nil {
		return nil
	}
	return redisCacheStore{rdb: rdb}
}

// cacheTTL is the lifetime of a result-cache entry. It MUST be kept at or below
// the outputs bucket's object TTL so the cache never points at a deleted
// object; the on-hit existence check is the backstop if they ever drift.
// Configurable via RESULT_CACHE_TTL_SECONDS; 0 (or negative) disables caching.
func cacheTTL() time.Duration {
	if v := os.Getenv("RESULT_CACHE_TTL_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return time.Duration(n) * time.Second
		}
	}
	return time.Hour
}

// buildCacheKey derives a deterministic cache key from the tool type, the
// canonicalised options, and the sorted input ETags. Identical inputs in any
// order yield the same key.
func buildCacheKey(toolType string, optionsRaw json.RawMessage, etags []string) string {
	sorted := append([]string(nil), etags...)
	sort.Strings(sorted)

	h := sha256.New()
	h.Write([]byte(toolType))
	h.Write([]byte{0})
	h.Write(canonicalOptions(optionsRaw))
	h.Write([]byte{0})
	for _, e := range sorted {
		h.Write([]byte(e))
		h.Write([]byte{0})
	}
	return cacheKeyPrefix + hex.EncodeToString(h.Sum(nil))
}

// canonicalOptions normalises an options blob so semantically equal options
// hash identically (Go's json.Marshal sorts map keys). Invalid JSON falls back
// to the raw bytes.
func canonicalOptions(optionsRaw json.RawMessage) []byte {
	if len(optionsRaw) == 0 {
		return []byte("{}")
	}
	var parsed interface{}
	if err := json.Unmarshal(optionsRaw, &parsed); err != nil {
		return optionsRaw
	}
	if b, err := json.Marshal(parsed); err == nil {
		return b
	}
	return optionsRaw
}

// inputETags fetches the ETag of every input object from the uploads bucket.
// A failure to stat any input returns an error and the caller skips caching
// (it must not fail the job).
func inputETags(ctx context.Context, store Storage, keys []string) ([]string, error) {
	etags := make([]string, 0, len(keys))
	for _, key := range keys {
		info, err := store.StatObject(ctx, store.BucketUploads(), key)
		if err != nil {
			return nil, err
		}
		etags = append(etags, info.ETag)
	}
	return etags, nil
}

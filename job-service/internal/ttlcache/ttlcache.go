// Package ttlcache is a small bounded, TTL-expiring in-memory cache. It exists
// because a plain sync.Map used as a process-lifetime cache grows without
// limit: entries whose rows are later deleted by the TTL sweep have no delete
// hook to reach them, so the map leaks on long-lived replicas.
package ttlcache

import (
	"sync"
	"time"
)

// entry is one cached value with its absolute expiry instant. Storing a deadline
// rather than an insertion time lets Get judge staleness on its own, which is
// what makes lazy expiry possible without a background sweeper goroutine.
type entry[V any] struct {
	value     V
	expiresAt time.Time
}

// Cache is a concurrency-safe map with per-entry TTL and a maximum size.
// Expiry is lazy (checked on Get) and the size cap is enforced on Set, so
// there is no janitor goroutine to start or stop — at these sizes a background
// sweeper would cost more than the stale entries it reclaims.
type Cache[K comparable, V any] struct {
	mu         sync.Mutex
	items      map[K]entry[V]
	ttl        time.Duration
	maxEntries int
	// now is swappable in tests to exercise expiry without sleeping.
	now func() time.Time
}

// New returns a Cache holding at most maxEntries items for ttl each.
func New[K comparable, V any](ttl time.Duration, maxEntries int) *Cache[K, V] {
	if maxEntries <= 0 {
		maxEntries = 1000
	}
	return &Cache[K, V]{
		items:      make(map[K]entry[V]),
		ttl:        ttl,
		maxEntries: maxEntries,
		now:        time.Now,
	}
}

// Get returns the cached value for key. Expired entries are treated as misses
// and dropped.
func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok {
		var zero V
		return zero, false
	}
	if c.now().After(e.expiresAt) {
		delete(c.items, key)
		var zero V
		return zero, false
	}
	return e.value, true
}

// Set stores value under key with the cache's TTL. When the cache is at
// capacity it first evicts expired entries; if that frees nothing, it drops
// the entry closest to expiry so Set never blocks and the map stays bounded.
func (c *Cache[K, V]) Set(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if _, exists := c.items[key]; !exists && len(c.items) >= c.maxEntries {
		c.evictLocked(now)
	}
	c.items[key] = entry[V]{value: value, expiresAt: now.Add(c.ttl)}
}

// Delete removes key. Callers must invoke this whenever the underlying row is
// deleted so a re-created job never reads a stale entry.
func (c *Cache[K, V]) Delete(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, key)
}

// Len reports the current number of entries (including not-yet-reaped expired
// ones). Used by tests and diagnostics.
func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// evictLocked drops expired entries, falling back to the entry nearest expiry
// when everything is still live. Caller must hold c.mu.
func (c *Cache[K, V]) evictLocked(now time.Time) {
	evicted := false
	for k, e := range c.items {
		if now.After(e.expiresAt) {
			delete(c.items, k)
			evicted = true
		}
	}
	if evicted {
		return
	}
	var oldestKey K
	var oldest time.Time
	first := true
	for k, e := range c.items {
		if first || e.expiresAt.Before(oldest) {
			oldestKey, oldest, first = k, e.expiresAt, false
		}
	}
	if !first {
		delete(c.items, oldestKey)
	}
}

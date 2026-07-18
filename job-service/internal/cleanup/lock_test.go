package cleanup

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"fyredocs/shared/redisstore"
)

func withMiniRedis(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	prev := redisstore.Client
	redisstore.Client = client
	t.Cleanup(func() { redisstore.Client = prev })
	return client
}

// TestReleaseCleanupLock_OnlyOwnerReleases verifies the compare-and-delete lock
// release (finding B3): a replica whose sweep outran the TTL must NOT delete a
// different replica's freshly-acquired lock.
func TestReleaseCleanupLock_OnlyOwnerReleases(t *testing.T) {
	client := withMiniRedis(t)
	ctx := context.Background()

	// Replica A holds the lock, then its TTL expires and replica B acquires it
	// (simulated by overwriting the stored token with B's).
	tokenA := "replica-a-token"
	tokenB := "replica-b-token"
	if err := client.Set(ctx, cleanupLockKey, tokenB, 10*time.Minute).Err(); err != nil {
		t.Fatalf("seed lock: %v", err)
	}

	// A's deferred release must be a no-op because the token no longer matches.
	releaseCleanupLock(tokenA)

	got, err := client.Get(ctx, cleanupLockKey).Result()
	if err != nil {
		t.Fatalf("lock should still exist after non-owner release: %v", err)
	}
	if got != tokenB {
		t.Fatalf("lock value = %q, want B's token %q (A must not delete B's lock)", got, tokenB)
	}

	// The real owner (B) releases successfully.
	releaseCleanupLock(tokenB)
	if _, err := client.Get(ctx, cleanupLockKey).Result(); err != redis.Nil {
		t.Fatalf("lock should be gone after the owner releases it, err = %v", err)
	}
}

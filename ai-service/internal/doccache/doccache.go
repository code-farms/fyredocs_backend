// Package doccache keeps a validated document's pages server-side for a short
// TTL, so a multi-turn Ask (or a second op on the same document) sends a
// docRef instead of re-uploading up to ~1 MiB of page text per turn from a
// phone. It also pins the exact document bytes across turns, which keeps the
// prompt prefix byte-identical — the condition for provider prefix caching.
//
// Entries are scoped per user: a ref is a content hash, so without the user
// in the key one user could read another's document by guessing a hash of
// common text.
package doccache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"ai-service/internal/ops"
)

// Store is the cache seam; Redis in production, nil disables caching.
type Store interface {
	Put(ctx context.Context, userID string, pages []ops.Page) (string, error)
	Get(ctx context.Context, userID, ref string) ([]ops.Page, bool, error)
}

// Redis implements Store with sliding expiry.
type Redis struct {
	Client *redis.Client
	TTL    time.Duration
}

// key builds the per-user cache key for ref.
func key(userID, ref string) string { return "ai:doc:" + userID + ":" + ref }

// Ref is the content hash of a page list — stable for identical documents.
func Ref(pages []ops.Page) (string, []byte, error) {
	data, err := json.Marshal(pages)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), data, nil
}

// Put stores pages and returns their ref. Re-putting the same document only
// refreshes its TTL.
func (r *Redis) Put(ctx context.Context, userID string, pages []ops.Page) (string, error) {
	ref, data, err := Ref(pages)
	if err != nil {
		return "", err
	}
	if err := r.Client.Set(ctx, key(userID, ref), data, r.TTL).Err(); err != nil {
		return "", err
	}
	return ref, nil
}

// Get returns the cached pages and slides the TTL; a miss is (nil, false, nil).
func (r *Redis) Get(ctx context.Context, userID, ref string) ([]ops.Page, bool, error) {
	data, err := r.Client.GetEx(ctx, key(userID, ref), r.TTL).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var pages []ops.Page
	if err := json.Unmarshal(data, &pages); err != nil {
		return nil, false, err
	}
	return pages, true, nil
}

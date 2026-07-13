package authverify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"time"

	"fyredocs/shared/config"
	"github.com/redis/go-redis/v9"
)

// GuestStore validates anonymous guest tokens so guest sessions can be admitted
// without a full JWT.
type GuestStore interface {
	ValidateGuestToken(ctx context.Context, token string) (bool, error)
}

// GuestStoreConfig customizes the Redis key layout for guest tokens.
type GuestStoreConfig struct {
	KeyPrefix string
	KeySuffix string
}

// RedisGuestStore is the Redis-backed GuestStore implementation.
type RedisGuestStore struct {
	client    *redis.Client
	keyPrefix string
	keySuffix string
}

// NewRedisGuestStore builds a RedisGuestStore with default key prefix/suffix
// when unset. Returns nil when client is nil so callers can treat guest support
// as disabled.
func NewRedisGuestStore(client *redis.Client, config GuestStoreConfig) *RedisGuestStore {
	if client == nil {
		return nil
	}
	prefix := config.KeyPrefix
	if prefix == "" {
		prefix = "guest"
	}
	suffix := config.KeySuffix
	if suffix == "" {
		suffix = "jobs"
	}
	return &RedisGuestStore{
		client:    client,
		keyPrefix: prefix,
		keySuffix: suffix,
	}
}

// ValidateGuestToken reports whether the guest token maps to a live Redis key.
// A nil store, nil client, or empty token is treated as invalid (not an error).
func (s *RedisGuestStore) ValidateGuestToken(ctx context.Context, token string) (bool, error) {
	if s == nil || s.client == nil || token == "" {
		return false, nil
	}
	key := fmt.Sprintf("%s:%s:%s", s.keyPrefix, token, s.keySuffix)
	result, err := s.client.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return result > 0, nil
}

// TokenDenylist tracks access tokens revoked before their natural expiry,
// enabling immediate cross-service logout/revocation.
type TokenDenylist interface {
	IsTokenDenied(ctx context.Context, token string) (bool, error)
	DenyToken(ctx context.Context, token string, ttl time.Duration) error
}

// RedisTokenDenylist is the Redis-backed TokenDenylist implementation. Tokens
// are stored by SHA-256 hash so raw tokens never land in Redis.
type RedisTokenDenylist struct {
	client    *redis.Client
	keyPrefix string
}

// NewRedisTokenDenylist builds a RedisTokenDenylist with a default key prefix
// when unset. Returns nil when client is nil so callers can treat the denylist
// as disabled.
func NewRedisTokenDenylist(client *redis.Client, keyPrefix string) *RedisTokenDenylist {
	if client == nil {
		return nil
	}
	if keyPrefix == "" {
		keyPrefix = "denylist:jwt"
	}
	return &RedisTokenDenylist{
		client:    client,
		keyPrefix: keyPrefix,
	}
}

// IsTokenDenied reports whether the token has been revoked. A nil denylist, nil
// client, or empty token is treated as not-denied (not an error).
func (d *RedisTokenDenylist) IsTokenDenied(ctx context.Context, token string) (bool, error) {
	if d == nil || d.client == nil || token == "" {
		return false, nil
	}
	key := d.key(token)
	result, err := d.client.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return result > 0, nil
}

// DenyToken revokes a token until ttl elapses, so the entry self-expires around
// the token's own expiry. A non-positive ttl falls back to 8 hours.
func (d *RedisTokenDenylist) DenyToken(ctx context.Context, token string, ttl time.Duration) error {
	if d == nil || d.client == nil || token == "" {
		return nil
	}
	key := d.key(token)
	if ttl <= 0 {
		ttl = 8 * time.Hour
	}
	return d.client.Set(ctx, key, "1", ttl).Err()
}

func (d *RedisTokenDenylist) key(token string) string {
	hash := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%s:%s", d.keyPrefix, hex.EncodeToString(hash[:]))
}

// NewRedisClientFromEnv connects a Redis client from REDIS_* environment
// variables and verifies it with a ping, returning an error if unreachable.
func NewRedisClientFromEnv() (*redis.Client, error) {
	addr := config.GetEnv("REDIS_ADDR", "redis:6379")
	password := os.Getenv("REDIS_PASSWORD")
	dbIndex := config.GetEnvInt("REDIS_DB", 0)

	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       dbIndex,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis connection failed: %w", err)
	}
	slog.Info("Auth redis connection established")
	return client, nil
}

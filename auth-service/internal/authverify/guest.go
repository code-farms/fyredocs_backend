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

type GuestStore interface {
	ValidateGuestToken(ctx context.Context, token string) (bool, error)
}

type GuestStoreConfig struct {
	KeyPrefix string
	KeySuffix string
}

type RedisGuestStore struct {
	client    *redis.Client
	keyPrefix string
	keySuffix string
}

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

type TokenDenylist interface {
	IsTokenDenied(ctx context.Context, token string) (bool, error)
	DenyToken(ctx context.Context, token string, ttl time.Duration) error
}

type RedisTokenDenylist struct {
	client    *redis.Client
	keyPrefix string
}

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


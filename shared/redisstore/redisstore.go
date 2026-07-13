// Package redisstore provides the shared Redis client used for rate limiting,
// caching, and guest/denylist storage. Connect initializes the package-global
// Client from REDIS_* environment variables.
package redisstore

import (
	"context"
	"log/slog"
	"os"
	"time"

	"fyredocs/shared/config"
	"github.com/redis/go-redis/v9"
)

// Client is the package-global Redis client, initialized by Connect.
var Client *redis.Client

// Connect initializes Client from REDIS_* environment variables and verifies it
// with a ping, exiting the process if Redis is unreachable.
func Connect() {
	addr := config.GetEnv("REDIS_ADDR", "redis:6379")
	password := os.Getenv("REDIS_PASSWORD")
	dbIndex := config.GetEnvInt("REDIS_DB", 0)

	Client = redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       dbIndex,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Client.Ping(ctx).Err(); err != nil {
		slog.Error("Failed to connect to redis", "error", err)
		os.Exit(1)
	}
	slog.Info("Redis connection established")
}

// Close releases the shared client if it was initialized; safe to call when nil.
func Close() {
	if Client != nil {
		_ = Client.Close()
	}
}

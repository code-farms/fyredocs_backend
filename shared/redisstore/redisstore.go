package redisstore

import (
	"context"
	"log/slog"
	"os"
	"time"

	"esydocs/shared/config"
	"github.com/redis/go-redis/v9"
)

var Client *redis.Client

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

func Close() {
	if Client != nil {
		_ = Client.Close()
	}
}


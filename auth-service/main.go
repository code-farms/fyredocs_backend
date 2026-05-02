package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"fyredocs/shared/config"
	"fyredocs/shared/logger"
	"fyredocs/shared/metrics"
	"fyredocs/shared/natsconn"
	"fyredocs/shared/telemetry"

	"auth-service/internal/authverify"
	"auth-service/internal/models"
	"auth-service/internal/token"
	"auth-service/routes"
)

func main() {
	config.LoadConfig()
	logger.Init("auth-service", os.Getenv("LOG_MODE"))
	shutdownTracer := telemetry.Init("auth-service")
	defer shutdownTracer(context.Background())

	if err := config.ValidateJWTSecret(); err != nil {
		slog.Error("JWT secret validation failed", "error", err)
		os.Exit(1)
	}

	models.Connect(models.PoolConfig{
		MaxOpenConns: 10,
		MaxIdleConns: 5,
	})
	models.Migrate()

	if err := natsconn.Connect(); err != nil {
		slog.Warn("NATS connection failed, analytics events will be skipped", "error", err)
	} else {
		defer natsconn.Close()
		if err := natsconn.EnsureStreams(context.Background()); err != nil {
			slog.Warn("NATS stream setup failed", "error", err)
		}
	}

	redisClient, err := authverify.NewRedisClientFromEnv()
	if err != nil {
		slog.Error("redis connection failed", "error", err)
		os.Exit(1)
	}
	defer redisClient.Close()

	issuer, err := token.NewIssuerFromEnv()
	if err != nil {
		slog.Error("auth issuer init failed", "error", err)
		os.Exit(1)
	}

	var denylist authverify.TokenDenylist
	if config.GetEnvBool("AUTH_DENYLIST_ENABLED", true) {
		denylist = authverify.NewRedisTokenDenylist(redisClient, os.Getenv("AUTH_DENYLIST_PREFIX"))
		if denylist == nil {
			slog.Warn("Token denylist enabled but Redis unavailable")
		} else {
			slog.Info("Token denylist enabled")
		}
	} else {
		slog.Warn("Token denylist disabled - logout will not revoke access tokens")
	}

	r := gin.Default()
	r.Use(telemetry.GinTraceMiddleware("auth-service"))
	r.Use(metrics.GinMetricsMiddleware())
	r.Use(logger.GinRequestID())
	r.Use(logger.GinRequestLogger())
	r.GET("/metrics", metrics.MetricsHandler())
	if err := r.SetTrustedProxies(config.TrustedProxies()); err != nil {
		slog.Error("failed to set trusted proxies", "error", err)
		os.Exit(1)
	}

	// Auth middleware applied selectively to protected routes only (not login/signup/refresh)
	authMiddleware := buildAuthMiddleware(redisClient, denylist)
	routes.SetupRouter(r, issuer, denylist, redisClient, authMiddleware)

	// Periodically clean up expired sessions from the database. Cancellable via
	// cleanupCtx and tracked by cleanupWG so SIGTERM drains an in-flight delete
	// instead of yanking the DB connection mid-statement.
	cleanupCtx, cancelCleanup := context.WithCancel(context.Background())
	defer cancelCleanup()
	var cleanupWG sync.WaitGroup
	cleanupWG.Add(1)
	go func() {
		defer cleanupWG.Done()
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-cleanupCtx.Done():
				return
			case <-ticker.C:
				deleted, err := models.DeleteExpiredSessions(models.DB)
				if err != nil {
					slog.Warn("expired session cleanup failed", "error", err)
				} else if deleted > 0 {
					slog.Info("cleaned up expired sessions", "count", deleted)
				}
			}
		}
	}()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8086"
	}

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	go func() {
		slog.Info("auth-service listening", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Info("shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("server forced to shutdown", "error", err)
	}
	cancelCleanup()
	cleanupWG.Wait()
	slog.Info("server exited")
}

func buildAuthMiddleware(redisClient *redis.Client, denylist authverify.TokenDenylist) gin.HandlerFunc {
	trustGateway := config.GetEnvBool("AUTH_TRUST_GATEWAY_HEADERS", false)

	verifier, err := authverify.NewVerifierFromEnv(denylist)
	if err != nil {
		slog.Error("auth verifier init failed", "error", err)
		os.Exit(1)
	}

	guestStore := authverify.NewRedisGuestStore(redisClient, authverify.GuestStoreConfig{
		KeyPrefix: os.Getenv("AUTH_GUEST_PREFIX"),
		KeySuffix: os.Getenv("AUTH_GUEST_SUFFIX"),
	})

	return authverify.GinAuthMiddleware(authverify.GinMiddlewareOptions{
		Verifier:            verifier,
		GuestStore:          guestStore,
		TrustGatewayHeaders: trustGateway,
	})
}

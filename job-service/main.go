package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"esydocs/shared/config"
	"esydocs/shared/logger"
	"esydocs/shared/metrics"
	"esydocs/shared/natsconn"
	"esydocs/shared/redisstore"
	"esydocs/shared/telemetry"

	"job-service/internal/authverify"
	"job-service/internal/models"
	"job-service/routes"
)

func validateJWTSecret() error {
	secret := os.Getenv("JWT_HS256_SECRET")
	if secret == "" {
		secret = os.Getenv("JWT_SECRET")
	}

	secret = strings.TrimSpace(secret)
	if secret == "" {
		return fmt.Errorf("JWT_HS256_SECRET environment variable is required but not set")
	}

	if len(secret) < 32 {
		return fmt.Errorf("JWT_HS256_SECRET must be at least 32 characters (256 bits), got %d characters", len(secret))
	}

	dangerousSecrets := []string{
		"4de0ea7311594deb860f03e5da60ac903fc4b4099bfe499a82e0fed013af32ca791ac065ea5e4d8aaade24a760e6dc58",
		"change-me",
		"secret",
		"password",
	}
	for _, dangerous := range dangerousSecrets {
		if secret == dangerous {
			return fmt.Errorf("JWT_HS256_SECRET appears to be a default/example value - use a cryptographically random secret")
		}
	}

	slog.Info("JWT secret validation passed")
	return nil
}

func main() {
	config.LoadConfig()
	logger.Init("job-service", os.Getenv("LOG_MODE"))
	shutdownTracer := telemetry.Init("job-service")
	defer shutdownTracer(context.Background())

	if err := validateJWTSecret(); err != nil {
		slog.Error("JWT secret validation failed", "error", err)
		os.Exit(1)
	}

	models.Connect(models.PoolConfig{
		MaxOpenConns: 20,
		MaxIdleConns: 10,
	})
	models.Migrate()
	redisstore.Connect()

	if err := natsconn.Connect(); err != nil {
		slog.Error("NATS connection failed", "error", err)
		os.Exit(1)
	}
	defer natsconn.Close()
	if err := natsconn.EnsureStreams(context.Background()); err != nil {
		slog.Error("NATS stream setup failed", "error", err)
		os.Exit(1)
	}

	// Initialize denylist for JWT verification
	var denylist authverify.TokenDenylist
	if getEnvBool("AUTH_DENYLIST_ENABLED", true) {
		denylist = authverify.NewRedisTokenDenylist(redisstore.Client, os.Getenv("AUTH_DENYLIST_PREFIX"))
		if denylist == nil {
			slog.Warn("Token denylist enabled but Redis unavailable")
		} else {
			slog.Info("Token denylist enabled")
		}
	} else {
		slog.Warn("Token denylist disabled")
	}

	r := gin.Default()
	r.Use(telemetry.GinTraceMiddleware("job-service"))
	r.Use(metrics.GinMetricsMiddleware())
	r.Use(logger.GinRequestID())
	r.Use(logger.GinRequestLogger())
	r.GET("/metrics", metrics.MetricsHandler())
	r.MaxMultipartMemory = 50 << 20
	if err := r.SetTrustedProxies(trustedProxies()); err != nil {
		slog.Error("failed to set trusted proxies", "error", err)
		os.Exit(1)
	}
	authMiddleware := buildAuthMiddleware(denylist)
	r.Use(authMiddleware)
	routes.SetupRouter(r)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	// Fix #14: Graceful shutdown
	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	go func() {
		slog.Info("job-service listening", "port", port)
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
	redisstore.Close()
	slog.Info("server exited")
}

func trustedProxies() []string {
	raw := strings.TrimSpace(os.Getenv("TRUSTED_PROXIES"))
	if raw == "" {
		return []string{"127.0.0.1", "::1"}
	}

	parts := strings.Split(raw, ",")
	proxies := make([]string, 0, len(parts))
	for _, part := range parts {
		proxy := strings.TrimSpace(part)
		if proxy != "" {
			proxies = append(proxies, proxy)
		}
	}
	if len(proxies) == 0 {
		return []string{"127.0.0.1", "::1"}
	}

	return proxies
}

func buildAuthMiddleware(denylist authverify.TokenDenylist) gin.HandlerFunc {
	trustGateway := getEnvBool("AUTH_TRUST_GATEWAY_HEADERS", false)

	verifier, err := authverify.NewVerifierFromEnv(denylist)
	if err != nil {
		slog.Error("auth verifier init failed", "error", err)
		os.Exit(1)
	}

	guestStore := authverify.NewRedisGuestStore(redisstore.Client, authverify.GuestStoreConfig{
		KeyPrefix: os.Getenv("AUTH_GUEST_PREFIX"),
		KeySuffix: os.Getenv("AUTH_GUEST_SUFFIX"),
	})

	return authverify.GinAuthMiddleware(authverify.GinMiddlewareOptions{
		Verifier:            verifier,
		GuestStore:          guestStore,
		TrustGatewayHeaders: trustGateway,
	})
}

func getEnvBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "y":
		return true
	case "0", "false", "no", "n":
		return false
	default:
		return fallback
	}
}

// Command user-service owns organizations and memberships, providing the
// role-based access control (RBAC) anchor for multi-tenant document ownership.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"fyredocs/shared/authverify"
	"fyredocs/shared/config"
	"fyredocs/shared/logger"
	"fyredocs/shared/metrics"
	"fyredocs/shared/redisstore"
	"fyredocs/shared/response"
	"fyredocs/shared/telemetry"

	"user-service/handlers"
	"user-service/internal/models"
	"user-service/routes"
)

// main boots the organizations and memberships, the platform's RBAC anchor.
//
// Order here is load-bearing: configuration and logging come up first so any
// later failure is diagnosable, then the database and its migrations, then the
// external brokers, and only then does the server start accepting traffic —
// binding the port before dependencies are ready would advertise readiness the
// service cannot honour. Every dependency failure calls os.Exit rather than
// degrading, so a misconfigured instance dies at boot instead of failing requests
// one at a time. Shutdown is the reverse: stop accepting, drain, then release.
func main() {
	handlers.ServiceStartTime = time.Now().UTC()
	config.LoadConfig()
	logger.Init("user-service", os.Getenv("LOG_MODE"))
	shutdownTracer := telemetry.Init("user-service")
	defer shutdownTracer(context.Background())

	models.Connect(models.PoolConfig{MaxOpenConns: 15, MaxIdleConns: 5})
	models.Migrate()

	// Redis backs the JWT denylist. Without it a token revoked at logout would
	// keep working here until natural expiry, even though the gateway rejects it.
	redisstore.Connect()
	defer redisstore.Close()

	var denylist authverify.TokenDenylist
	if config.GetEnvBool("AUTH_DENYLIST_ENABLED", true) {
		denylist = authverify.NewRedisTokenDenylist(redisstore.Client, os.Getenv("AUTH_DENYLIST_PREFIX"))
		if denylist == nil {
			slog.Warn("Token denylist enabled but Redis unavailable")
		} else {
			slog.Info("Token denylist enabled")
		}
	} else {
		slog.Warn("Token denylist disabled")
	}

	r := gin.New()
	r.Use(response.GinRecovery())
	r.Use(telemetry.GinTraceMiddleware("user-service"))
	r.Use(metrics.GinMetricsMiddleware())
	r.Use(logger.GinRequestID())
	r.Use(logger.GinRequestLogger())
	// Registered before the auth middleware so Prometheus can scrape without a
	// token — Gin binds a route's chain at registration, so a later r.Use does
	// not apply retroactively. Moving this below the r.Use call breaks scraping.
	r.GET("/metrics", metrics.MetricsHandler())
	if err := r.SetTrustedProxies(config.TrustedProxies()); err != nil {
		slog.Error("failed to set trusted proxies", "error", err)
		os.Exit(1)
	}
	r.Use(buildAuthMiddleware(denylist))

	routes.SetupRouter(r)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8090"
	}

	srv := &http.Server{Addr: ":" + port, Handler: r}
	config.ApplyServerTimeouts(srv, false)

	go func() {
		slog.Info("user-service listening", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Info("shutting down user-service...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("server forced to shutdown", "error", err)
	}
	slog.Info("user-service exited")
}

// buildAuthMiddleware assembles this service's auth middleware.
//
// AUTH_TRUST_GATEWAY_HEADERS decides whether identity headers injected upstream
// are believed. It defaults to FALSE, which is the safe default: trusting those
// headers is only sound when nothing but the gateway can reach this service, and
// a service reachable across a host boundary would otherwise accept a
// caller-supplied X-User-ID verbatim. With it off, the JWT is verified here
// regardless of what the gateway claims.
//
// This middleware resolves identity but does not gate — credential-less requests
// pass through anonymously so /healthz, /readyz, and the shared-secret
// /internal/v1 routes still work. RequireUser does the gating on /api.
//
// GuestStore is deliberately nil — organizations and memberships are user-owned,
// so a guest identity carries no UserID and RequireUser rejects it anyway.
//
// NOTE: intentionally duplicated per service rather than shared (CLAUDE.md §1);
// job-service/main.go and auth-service/main.go carry the same function. A
// verifier that cannot be constructed exits the process — booting without working
// token verification would serve traffic with authentication silently disabled.
func buildAuthMiddleware(denylist authverify.TokenDenylist) gin.HandlerFunc {
	trustGateway := config.GetEnvBool("AUTH_TRUST_GATEWAY_HEADERS", false)

	verifier, err := authverify.NewVerifierFromEnv(denylist)
	if err != nil {
		slog.Error("auth verifier init failed", "error", err)
		os.Exit(1)
	}

	return authverify.GinAuthMiddleware(authverify.GinMiddlewareOptions{
		Verifier:            verifier,
		TrustGatewayHeaders: trustGateway,
	})
}

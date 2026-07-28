// Command analytics-service ingests platform events and metrics and serves the
// user dashboard and the admin analytics API. It consumes analytics events from
// NATS, samples gateway HTTP metrics, and scrapes Prometheus for system health.
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
	"fyredocs/shared/natsconn"
	"fyredocs/shared/redisstore"
	"fyredocs/shared/response"
	"fyredocs/shared/telemetry"

	"analytics-service/handlers"
	"analytics-service/internal/apisampler"
	"analytics-service/internal/models"
	"analytics-service/internal/opsmetrics"
	"analytics-service/routes"
	"analytics-service/subscriber"
)

// main boots the event ingest and the dashboard/admin analytics API.
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
	logger.Init("analytics-service", os.Getenv("LOG_MODE"))
	shutdownTracer := telemetry.Init("analytics-service")
	defer shutdownTracer(context.Background())

	// The dashboard handler fans out up to ~10 independent queries concurrently
	// (the DB is remote, so this collapses many sequential round-trips into one).
	// Keep enough idle connections warm to serve a full fan-out without opening
	// fresh connections — a new connection to a remote Postgres pays a TLS
	// handshake round-trip that would otherwise erode the parallelism gain.
	models.Connect(models.PoolConfig{
		MaxOpenConns: 20,
		MaxIdleConns: 15,
	})
	models.Migrate()

	// Redis backs the short-TTL /api/dashboard response cache.
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

	subs, err := subscriber.Start(context.Background())
	if err != nil {
		slog.Error("failed to start analytics subscribers", "error", err)
		os.Exit(1)
	}

	// Periodically sample the gateway's Prometheus metrics into api_metric_samples
	// so /admin/metrics/api-trends can render a time series (counters alone have no
	// history). Runs for the lifetime of the process.
	samplerCtx, stopSampler := context.WithCancel(context.Background())
	defer stopSampler()
	samplerDone := apisampler.Start(samplerCtx, models.DB)

	// Ops-health poller: exposes dependency_up{redis,nats,postgres} and
	// nats_dlq_pending_messages on /metrics for the Prometheus alert rules.
	opsCtx, stopOps := context.WithCancel(context.Background())
	defer stopOps()
	opsDone := opsmetrics.Start(opsCtx, models.DB)

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
	r.Use(telemetry.GinTraceMiddleware("analytics-service"))
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
		port = "8087"
	}

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}
	config.ApplyServerTimeouts(srv, false)

	go func() {
		slog.Info("analytics-service listening", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Info("shutting down analytics-service...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("server forced to shutdown", "error", err)
	}
	subs.Stop()

	// Stop the background pollers and JOIN them, so an in-flight metric write
	// or prune finishes instead of being abandoned mid-statement.
	stopSampler()
	stopOps()
	joinPoller("apisampler", samplerDone)
	joinPoller("opsmetrics", opsDone)
	slog.Info("analytics-service exited")
}

// joinPoller waits (bounded) for a background poller to finish after its
// context has been cancelled, so shutdown never abandons an in-flight DB write.
func joinPoller(name string, done <-chan struct{}) {
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		slog.Warn("poller did not stop in time", "poller", name)
	}
}

// buildAuthMiddleware assembles this service's auth middleware.
//
// AUTH_TRUST_GATEWAY_HEADERS decides whether identity headers injected upstream
// are believed. It defaults to FALSE, which is the safe default: trusting those
// headers is only sound when nothing but the gateway can reach this service, and
// a service reachable across a host boundary would otherwise accept a
// caller-supplied X-User-Role verbatim — here that means anyone who can reach the
// port could claim super-admin and read platform-wide metrics. With it off, the
// JWT is verified here regardless of what the gateway claims.
//
// This middleware resolves identity but does not gate — credential-less requests
// pass through anonymously. That is load-bearing for two routes: /healthz and
// /readyz (healthchecks send no token) and POST /internal/alerts/api/v2/alerts,
// which Prometheus calls and which cannot carry a token at all. adminAuth and the
// Dashboard handler do the gating.
//
// GuestStore is deliberately nil: the Dashboard handler rejects role "guest"
// outright, so resolving guest tokens here would only add a Redis round-trip.
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

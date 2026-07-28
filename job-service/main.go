// Command job-service is the orchestration hub for document processing. It
// handles presigned uploads, creates processing jobs, routes each to the owning
// worker service over NATS, streams progress via SSE, serves job downloads, and
// runs a TTL sweep that reaps expired jobs and files.
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

	"fyredocs/shared/config"
	"fyredocs/shared/logger"
	"fyredocs/shared/metrics"
	"fyredocs/shared/natsconn"
	"fyredocs/shared/redisstore"
	"fyredocs/shared/response"
	"fyredocs/shared/storage"
	"fyredocs/shared/telemetry"

	"fyredocs/shared/authverify"
	"job-service/handlers"
	"job-service/internal/cleanup"
	"job-service/internal/models"
	"job-service/routes"
)

// main boots the orchestration hub: presigned uploads, job creation, dispatch to workers, and live progress.
//
// Order here is load-bearing: configuration and logging come up first so any
// later failure is diagnosable, then the database and its migrations, then the
// external brokers, and only then does the server start accepting traffic —
// binding the port before dependencies are ready would advertise readiness the
// service cannot honour. Every dependency failure calls os.Exit rather than
// degrading, so a misconfigured instance dies at boot instead of failing requests
// one at a time. Shutdown is the reverse: stop accepting, drain, then release.
func main() {
	config.LoadConfig()
	logger.Init("job-service", os.Getenv("LOG_MODE"))
	shutdownTracer := telemetry.Init("job-service")
	defer shutdownTracer(context.Background())

	if err := config.ValidateJWTSecret(); err != nil {
		slog.Error("JWT secret validation failed", "error", err)
		os.Exit(1)
	}

	// Pool sized for the presigned-upload protocol: file bytes no longer
	// stream through this service, so handlers are short DB-bound requests
	// and the service sustains far more concurrent requests per replica.
	models.Connect(models.PoolConfig{
		MaxOpenConns: 50,
		MaxIdleConns: 25,
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

	// Object storage (MinIO/S3) is load-bearing for every upload, job
	// creation, and download — fail fast at boot if it is misconfigured.
	objStore, err := storage.NewFromEnv()
	if err != nil {
		slog.Error("object storage init failed", "error", err)
		os.Exit(1)
	}
	storageCtx, storageCancel := context.WithTimeout(context.Background(), 15*time.Second)
	for _, bucket := range []string{objStore.BucketUploads(), objStore.BucketOutputs()} {
		if err := objStore.EnsureBucket(storageCtx, bucket); err != nil {
			storageCancel()
			slog.Error("object storage bucket setup failed", "bucket", bucket, "error", err)
			os.Exit(1)
		}
	}
	storageCancel()
	handlers.SetObjectStore(objStore)

	// Background TTL sweeps (expired jobs, upload sessions, stale multipart
	// uploads) run in-process — job-service owns all the data they touch, so
	// the sweep ships in the same container as the API. CLEANUP_ENABLED=false
	// opts a replica out, keeping exactly one sweeper if the API ever scales
	// beyond a single replica.
	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	defer cleanupCancel()
	// cleanupDone lets shutdown JOIN the loop so an in-flight sweep finishes
	// its DB/object deletes instead of being abandoned mid-transaction.
	cleanupDone := make(chan struct{})
	if config.GetEnvBool("CLEANUP_ENABLED", true) {
		go func() {
			defer close(cleanupDone)
			runCleanupLoop(cleanupCtx, objStore)
		}()
	} else {
		close(cleanupDone)
		slog.Warn("cleanup loop disabled (CLEANUP_ENABLED=false)")
	}

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
	r.Use(telemetry.GinTraceMiddleware("job-service"))
	r.Use(metrics.GinMetricsMiddleware())
	r.Use(logger.GinRequestID())
	r.Use(logger.GinRequestLogger())
	r.GET("/metrics", metrics.MetricsHandler())
	r.MaxMultipartMemory = 50 << 20
	if err := r.SetTrustedProxies(config.TrustedProxies()); err != nil {
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

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}
	// streaming=true: job-service serves SSE job-event streams, so WriteTimeout
	// must stay unset to avoid severing long-lived connections.
	config.ApplyServerTimeouts(srv, true)

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
	cleanupCancel()
	// Join the cleanup loop (bounded) so an in-flight sweep can finish.
	select {
	case <-cleanupDone:
	case <-time.After(30 * time.Second):
		slog.Warn("cleanup loop did not stop in time")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("server forced to shutdown", "error", err)
	}
	redisstore.Close()
	slog.Info("server exited")
}

// runCleanupLoop executes cleanup.RunSweep immediately and then on every
// CLEANUP_INTERVAL tick until ctx is cancelled. This was the standalone
// cleanup-worker binary (cmd/cleanup) before being folded in so the service
// ships as a single container.
func runCleanupLoop(ctx context.Context, store cleanup.ObjectStore) {
	interval := config.CleanupInterval()
	slog.Info("cleanup loop started", "interval", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		cleanup.RunSweep(ctx, store)
		select {
		case <-ctx.Done():
			slog.Info("cleanup loop stopped")
			return
		case <-ticker.C:
		}
	}
}

// buildAuthMiddleware assembles this service's auth middleware.
//
// AUTH_TRUST_GATEWAY_HEADERS decides whether identity headers injected upstream
// are believed. It defaults to FALSE, which is the safe default: trusting those
// headers is only sound when nothing but the gateway can reach this service, and
// a service accidentally exposed directly would otherwise accept a
// caller-supplied X-User-ID verbatim. With it off, the JWT is verified here
// regardless of what the gateway claims.
//
// A verifier that cannot be constructed exits the process — booting without
// working token verification would serve traffic with authentication silently
// disabled.
func buildAuthMiddleware(denylist authverify.TokenDenylist) gin.HandlerFunc {
	trustGateway := config.GetEnvBool("AUTH_TRUST_GATEWAY_HEADERS", false)

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

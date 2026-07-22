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
	if config.GetEnvBool("CLEANUP_ENABLED", true) {
		go runCleanupLoop(cleanupCtx, objStore)
	} else {
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

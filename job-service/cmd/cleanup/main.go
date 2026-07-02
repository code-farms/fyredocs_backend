// The cleanup binary runs job-service's TTL sweep (expired jobs, upload
// sessions, stale multipart uploads) on an interval. It ships as its own
// container so heavy scans and batch deletes never compete with the API
// binary for CPU, but it is part of the job-service module and reuses its
// models — job-service owns all the data this binary touches.
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
	"fyredocs/shared/redisstore"
	"fyredocs/shared/storage"
	"fyredocs/shared/telemetry"

	"job-service/internal/cleanup"
	"job-service/internal/models"
)

func main() {
	config.LoadConfig()
	logger.Init("cleanup-worker", os.Getenv("LOG_MODE"))
	shutdownTracer := telemetry.Init("cleanup-worker")
	defer shutdownTracer(context.Background())

	// The API binary owns schema migration; this binary only needs a small
	// read/delete pool.
	models.Connect(models.PoolConfig{MaxOpenConns: 5, MaxIdleConns: 2})
	redisstore.Connect()

	// Object storage is a hard dependency: every cleanup phase that touches
	// file data goes through MinIO/S3, so fail fast if it is missing.
	store, err := storage.NewFromEnv()
	if err != nil {
		slog.Error("object storage init failed", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start HTTP server for health checks and metrics
	r := gin.New()
	r.Use(telemetry.GinTraceMiddleware("cleanup-worker"))
	r.Use(metrics.GinMetricsMiddleware())
	r.Use(logger.GinRequestID())
	r.Use(logger.GinRequestLogger())
	r.Use(gin.Recovery())
	r.GET("/metrics", metrics.MetricsHandler())
	if err := r.SetTrustedProxies(config.TrustedProxies()); err != nil {
		slog.Error("failed to set trusted proxies", "error", err)
		os.Exit(1)
	}

	r.GET("/healthz", func(c *gin.Context) {
		hctx, hcancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer hcancel()
		if err := redisstore.Client.Ping(hctx).Err(); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "redis": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "healthy"})
	})

	r.GET("/readyz", func(c *gin.Context) {
		checks := gin.H{}
		ready := true

		hctx, hcancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer hcancel()

		if err := redisstore.Client.Ping(hctx).Err(); err != nil {
			checks["redis"] = err.Error()
			ready = false
		} else {
			checks["redis"] = "ok"
		}

		if err := models.DB.Exec("SELECT 1").Error; err != nil {
			checks["postgres"] = err.Error()
			ready = false
		} else {
			checks["postgres"] = "ok"
		}

		if !ready {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready", "checks": checks})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready", "checks": checks})
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8088"
	}

	srv := &http.Server{Addr: ":" + port, Handler: r}
	config.ApplyServerTimeouts(srv, false)
	go func() {
		slog.Info("cleanup-worker HTTP server listening", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server failed", "error", err)
			os.Exit(1)
		}
	}()

	// Start cleanup loop
	interval := config.CleanupInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	slog.Info("cleanup-worker started", "interval", interval)

	go func() {
		for {
			cleanup.RunSweep(ctx, store)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	// Wait for shutdown signal
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	slog.Info("shutting down")
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown error", "error", err)
	}
}

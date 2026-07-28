// Command optimize-pdf is a processing worker for heavy PDF operations —
// Ghostscript compression and repair, and Tesseract OCR. It consumes dispatched
// jobs from NATS and reports completion through the shared worker loop.
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
	"github.com/google/uuid"

	"fyredocs/shared/config"
	"fyredocs/shared/logger"
	"fyredocs/shared/metrics"
	"fyredocs/shared/natsconn"
	"fyredocs/shared/redisstore"
	"fyredocs/shared/response"
	"fyredocs/shared/storage"
	"fyredocs/shared/telemetry"

	"optimize-pdf/internal/models"
	"optimize-pdf/internal/worker"
	"optimize-pdf/processing"
)

// main boots the Ghostscript/Tesseract worker for compression, repair, and OCR.
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
	logger.Init("optimize-pdf", os.Getenv("LOG_MODE"))
	shutdownTracer := telemetry.Init("optimize-pdf")
	defer shutdownTracer(context.Background())
	models.Connect()
	// Shared tables (processing_jobs, file_metadata) are migrated only by
	// job-service, their schema owner — see models/database.go.
	redisstore.Connect()

	store, err := storage.NewFromEnv()
	if err != nil {
		slog.Error("object storage init failed", "error", err)
		os.Exit(1)
	}

	if err := natsconn.Connect(); err != nil {
		slog.Error("NATS connection failed", "error", err)
		os.Exit(1)
	}
	defer natsconn.Close()
	if err := natsconn.EnsureStreams(context.Background()); err != nil {
		slog.Error("NATS stream setup failed", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	processFunc := func(ctx context.Context, jobID uuid.UUID, toolType string, inputPaths []string, options map[string]interface{}, outputDir string) (*worker.ProcessResult, error) {
		result, err := processing.ProcessFile(ctx, jobID, toolType, inputPaths, options, outputDir)
		if err != nil {
			return nil, err
		}
		return &worker.ProcessResult{OutputPath: result.OutputPath, Metadata: result.Metadata}, nil
	}

	// workerDone lets shutdown JOIN the worker: worker.Run drains in-flight
	// jobs (bounded by WORKER_DRAIN_TIMEOUT) before main is allowed to exit,
	// so a deploy never kills a PDF conversion mid-write.
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		worker.Run(ctx, worker.WorkerConfig{
			ServiceName: "optimize-pdf",
			AllowedTools: map[string]bool{
				"compress-pdf": true,
				"repair-pdf":   true,
				"ocr-pdf":      true,
			},
			Process:     processFunc,
			JS:          natsconn.JS,
			RedisClient: redisstore.Client,
			DB:          models.DB,
			Storage:     store,
		})
	}()

	r := gin.New()
	r.Use(telemetry.GinTraceMiddleware("optimize-pdf"))
	r.Use(metrics.GinMetricsMiddleware())
	r.Use(logger.GinRequestID())
	r.Use(logger.GinRequestLogger())
	r.Use(response.GinRecovery())
	r.GET("/metrics", metrics.MetricsHandler())
	if err := r.SetTrustedProxies(config.TrustedProxies()); err != nil {
		slog.Error("failed to set trusted proxies", "error", err)
		os.Exit(1)
	}

	// Liveness: is the process up? Must NOT check dependencies — a Redis/NATS
	// blip must not flip the container unhealthy and trigger a restart cascade.
	// Dependency health lives in /readyz below.
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Readiness probe: reports 503 unless Redis, NATS, and Postgres are all reachable.
	r.GET("/readyz", func(c *gin.Context) {
		checks := gin.H{}
		ready := true

		hctx, hcancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer hcancel()

		if err := redisstore.Client.Ping(hctx).Err(); err != nil {
			// Raw driver errors can leak internal host:port details; the probe
			// body stays generic and the cause goes to logs.
			slog.Warn("readyz dependency check failed", "dependency", "redis", "error", err)
			checks["redis"] = "unavailable"
			ready = false
		} else {
			checks["redis"] = "ok"
		}

		if natsconn.Conn == nil || !natsconn.Conn.IsConnected() {
			checks["nats"] = "disconnected"
			ready = false
		} else {
			checks["nats"] = "ok"
		}

		if err := models.DB.Exec("SELECT 1").Error; err != nil {
			// Raw driver errors can leak internal host:port details; the probe
			// body stays generic and the cause goes to logs.
			slog.Warn("readyz dependency check failed", "dependency", "postgres", "error", err)
			checks["postgres"] = "unavailable"
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
		port = "8085"
	}

	srv := &http.Server{Addr: ":" + port, Handler: r}
	config.ApplyServerTimeouts(srv, false)
	go func() {
		slog.Info("optimize-pdf listening", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	slog.Info("shutting down")
	cancel()
	// Join the worker's bounded drain (WORKER_DRAIN_TIMEOUT + margin as a
	// safety net). Compose's stop_grace_period must exceed this or Docker
	// SIGKILLs the process mid-drain.
	select {
	case <-workerDone:
		slog.Info("worker drained")
	case <-time.After(3 * time.Minute):
		slog.Error("worker did not stop in time; exiting anyway")
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown error", "error", err)
	}
}

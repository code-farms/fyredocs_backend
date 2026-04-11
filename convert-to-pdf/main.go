package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"esydocs/shared/config"
	"esydocs/shared/logger"
	"esydocs/shared/metrics"
	"esydocs/shared/natsconn"
	"esydocs/shared/redisstore"
	"esydocs/shared/telemetry"

	"convert-to-pdf/internal/models"
	"convert-to-pdf/internal/worker"
	"convert-to-pdf/processing"
)

func main() {
	config.LoadConfig()
	logger.Init("convert-to-pdf", os.Getenv("LOG_MODE"))
	shutdownTracer := telemetry.Init("convert-to-pdf")
	defer shutdownTracer(context.Background())
	models.Connect()
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	processFunc := func(ctx context.Context, jobID uuid.UUID, toolType string, inputPaths []string, options map[string]interface{}, outputDir string, onProgress worker.ProgressFunc) (*worker.ProcessResult, error) {
		// Adapt worker.ProgressFunc to processing.ProgressFunc (same signature).
		var pf processing.ProgressFunc
		if onProgress != nil {
			pf = processing.ProgressFunc(onProgress)
		}
		result, err := processing.ProcessFile(ctx, jobID, toolType, inputPaths, options, outputDir, pf)
		if err != nil {
			return nil, err
		}
		return &worker.ProcessResult{OutputPath: result.OutputPath, Metadata: result.Metadata}, nil
	}

	go worker.Run(ctx, worker.WorkerConfig{
		ServiceName: "convert-to-pdf",
		AllowedTools: map[string]bool{
			"word-to-pdf":  true,
			"ppt-to-pdf":   true,
			"excel-to-pdf": true,
			"html-to-pdf":  true,
			"image-to-pdf": true, "img-to-pdf": true,
		},
		Process:     processFunc,
		JS:          natsconn.JS,
		RedisClient: redisstore.Client,
		DB:          models.DB,
	})

	r := gin.New()
	r.Use(telemetry.GinTraceMiddleware("convert-to-pdf"))
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
		if natsconn.Conn == nil || !natsconn.Conn.IsConnected() {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "nats": "disconnected"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "healthy"})
	})

	r.GET("/readyz", func(c *gin.Context) {
		checks := gin.H{}
		ready := true

		hctx, hcancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer hcancel()

		// Check Redis
		if err := redisstore.Client.Ping(hctx).Err(); err != nil {
			checks["redis"] = err.Error()
			ready = false
		} else {
			checks["redis"] = "ok"
		}

		// Check NATS
		if natsconn.Conn == nil || !natsconn.Conn.IsConnected() {
			checks["nats"] = "disconnected"
			ready = false
		} else {
			checks["nats"] = "ok"
		}

		// Check DB
		if err := models.DB.Exec("SELECT 1").Error; err != nil {
			checks["postgres"] = err.Error()
			ready = false
		} else {
			checks["postgres"] = "ok"
		}

		// Check unoserver (informational — does not affect readiness since
		// officeToPDF falls back to direct LibreOffice invocation).
		unoPort := os.Getenv("UNOSERVER_PORT")
		if unoPort == "" {
			unoPort = "2002"
		}
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+unoPort, 1*time.Second)
		if err != nil {
			checks["unoserver"] = "unavailable (fallback active)"
		} else {
			conn.Close()
			checks["unoserver"] = "ok"
		}

		if !ready {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready", "checks": checks})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready", "checks": checks})
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8083"
	}

	srv := &http.Server{Addr: ":" + port, Handler: r}
	go func() {
		slog.Info("convert-to-pdf listening", "port", port)
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
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown error", "error", err)
	}
}


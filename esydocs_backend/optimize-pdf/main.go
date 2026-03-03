package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
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

	"optimize-pdf/internal/models"
	"optimize-pdf/internal/worker"
	"optimize-pdf/processing"
)

func main() {
	config.LoadConfig()
	logger.Init("optimize-pdf", os.Getenv("LOG_MODE"))
	shutdownTracer := telemetry.Init("optimize-pdf")
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

	processFunc := func(ctx context.Context, jobID uuid.UUID, toolType string, inputPaths []string, options map[string]interface{}, outputDir string) (*worker.ProcessResult, error) {
		result, err := processing.ProcessFile(ctx, jobID, toolType, inputPaths, options, outputDir)
		if err != nil {
			return nil, err
		}
		return &worker.ProcessResult{OutputPath: result.OutputPath, Metadata: result.Metadata}, nil
	}

	go worker.Run(ctx, worker.WorkerConfig{
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
	})

	r := gin.New()
	r.Use(telemetry.GinTraceMiddleware("optimize-pdf"))
	r.Use(metrics.GinMetricsMiddleware())
	r.Use(logger.GinRequestID())
	r.Use(logger.GinRequestLogger())
	r.Use(gin.Recovery())
	r.GET("/metrics", metrics.MetricsHandler())
	if err := r.SetTrustedProxies(trustedProxies()); err != nil {
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

	port := os.Getenv("PORT")
	if port == "" {
		port = "8085"
	}

	srv := &http.Server{Addr: ":" + port, Handler: r}
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
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown error", "error", err)
	}
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

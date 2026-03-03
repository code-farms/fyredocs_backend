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
	"esydocs/shared/database"
	"esydocs/shared/pdfhandlers"
	"esydocs/shared/redisstore"
	"esydocs/shared/worker"

	"optimize-pdf/processing"
)

func main() {
	config.LoadConfig()
	database.Connect()
	database.Migrate()
	redisstore.Connect()

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
		RedisClient: redisstore.Client,
		DB:          database.DB,
	})

	r := gin.New()
	r.Use(gin.Recovery())
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
		c.JSON(http.StatusOK, gin.H{"status": "healthy"})
	})

	h := pdfhandlers.NewHandlers(pdfhandlers.HandlerConfig{
		SupportedTools: map[string]bool{
			"compress-pdf": true,
			"repair-pdf":   true,
			"ocr-pdf":      true,
		},
		DB: database.DB,
	})
	api := r.Group("/api/optimize-pdf")
	h.RegisterRoutes(api)

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

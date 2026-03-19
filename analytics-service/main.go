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

	"esydocs/shared/config"
	"esydocs/shared/logger"
	"esydocs/shared/metrics"
	"esydocs/shared/natsconn"
	"esydocs/shared/telemetry"

	"analytics-service/internal/models"
	"analytics-service/routes"
	"analytics-service/subscriber"
)

func main() {
	config.LoadConfig()
	logger.Init("analytics-service", os.Getenv("LOG_MODE"))
	shutdownTracer := telemetry.Init("analytics-service")
	defer shutdownTracer(context.Background())

	models.Connect(models.PoolConfig{
		MaxOpenConns: 10,
		MaxIdleConns: 5,
	})
	models.Migrate()

	if err := natsconn.Connect(); err != nil {
		slog.Error("NATS connection failed", "error", err)
		os.Exit(1)
	}
	defer natsconn.Close()
	if err := natsconn.EnsureStreams(context.Background()); err != nil {
		slog.Error("NATS stream setup failed", "error", err)
		os.Exit(1)
	}

	if err := subscriber.Start(context.Background()); err != nil {
		slog.Error("failed to start analytics subscribers", "error", err)
		os.Exit(1)
	}

	r := gin.Default()
	r.Use(telemetry.GinTraceMiddleware("analytics-service"))
	r.Use(metrics.GinMetricsMiddleware())
	r.Use(logger.GinRequestID())
	r.Use(logger.GinRequestLogger())
	r.GET("/metrics", metrics.MetricsHandler())
	if err := r.SetTrustedProxies(trustedProxies()); err != nil {
		slog.Error("failed to set trusted proxies", "error", err)
		os.Exit(1)
	}

	routes.SetupRouter(r)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8087"
	}

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

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
	slog.Info("analytics-service exited")
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

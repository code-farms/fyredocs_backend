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
	"fyredocs/shared/telemetry"

	"user-service/handlers"
	"user-service/internal/models"
	"user-service/routes"
)

func main() {
	handlers.ServiceStartTime = time.Now().UTC()
	config.LoadConfig()
	logger.Init("user-service", os.Getenv("LOG_MODE"))
	shutdownTracer := telemetry.Init("user-service")
	defer shutdownTracer(context.Background())

	models.Connect(models.PoolConfig{MaxOpenConns: 15, MaxIdleConns: 5})
	models.Migrate()

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(telemetry.GinTraceMiddleware("user-service"))
	r.Use(metrics.GinMetricsMiddleware())
	r.Use(logger.GinRequestID())
	r.Use(logger.GinRequestLogger())
	r.GET("/metrics", metrics.MetricsHandler())
	if err := r.SetTrustedProxies(config.TrustedProxies()); err != nil {
		slog.Error("failed to set trusted proxies", "error", err)
		os.Exit(1)
	}

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

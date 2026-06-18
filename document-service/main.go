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
	"fyredocs/shared/telemetry"

	"document-service/handlers"
	"document-service/internal/models"
	"document-service/routes"
	"document-service/subscriber"
)

func main() {
	handlers.ServiceStartTime = time.Now().UTC()
	config.LoadConfig()
	logger.Init("document-service", os.Getenv("LOG_MODE"))
	shutdownTracer := telemetry.Init("document-service")
	defer shutdownTracer(context.Background())

	models.Connect(models.PoolConfig{MaxOpenConns: 15, MaxIdleConns: 5})
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
	subs, err := subscriber.Start(context.Background())
	if err != nil {
		slog.Error("failed to start finalize subscriber", "error", err)
		os.Exit(1)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(telemetry.GinTraceMiddleware("document-service"))
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
		port = "8089"
	}

	srv := &http.Server{Addr: ":" + port, Handler: r}

	go func() {
		slog.Info("document-service listening", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Info("shutting down document-service...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("server forced to shutdown", "error", err)
	}
	subs.Stop()
	slog.Info("document-service exited")
}

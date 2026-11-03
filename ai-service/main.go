// Command ai-service is the server-side proxy for every AI feature. It owns
// the prompt templates, input limits and the per-user token quota, and talks to the model provider over the OpenAI-compatible wire
// protocol — so the provider is configuration, and no key ever ships in a
// client.
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

	"fyredocs/shared/authverify"
	"fyredocs/shared/config"
	"fyredocs/shared/logger"
	"fyredocs/shared/metrics"
	"fyredocs/shared/natsconn"
	"fyredocs/shared/queue"
	"fyredocs/shared/redisstore"
	"fyredocs/shared/response"
	"fyredocs/shared/telemetry"

	"ai-service/handlers"
	"ai-service/internal/doccache"
	"ai-service/internal/llm"
	"ai-service/internal/models"
	"ai-service/internal/quota"
	"ai-service/routes"
)

// main boots the AI op proxy.
//
// Boot order follows notification-service/main.go: config and logging first,
// then the database and migrations, then brokers, and only then the listener.
// NATS is publish-only here (analytics), and unlike the database it is not
// fatal: an AI answer must not fail because a metrics event could not be
// recorded, so a missing JetStream disables analytics with a warning.
func main() {
	handlers.ServiceStartTime = time.Now().UTC()
	config.LoadConfig()
	logger.Init("ai-service", os.Getenv("LOG_MODE"))
	shutdownTracer := telemetry.Init("ai-service")
	defer shutdownTracer(context.Background())

	models.Connect(models.PoolConfig{MaxOpenConns: 10, MaxIdleConns: 3})
	models.Migrate()

	var publish handlers.Publisher
	if err := natsconn.Connect(); err != nil {
		slog.Warn("NATS unavailable — AI analytics disabled", "error", err)
	} else {
		defer natsconn.Close()
		if err := natsconn.EnsureStreams(context.Background()); err != nil {
			slog.Warn("NATS stream setup failed — AI analytics disabled", "error", err)
		} else {
			publish = func(ctx context.Context, e queue.AnalyticsEvent) error {
				return queue.PublishAnalyticsEvent(ctx, natsconn.JS, e)
			}
		}
	}

	// Redis backs the JWT denylist — so a token revoked at logout stops
	// spending AI quota here too — and the short-TTL document cache.
	redisstore.Connect()
	defer redisstore.Close()

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

	// The provider is pure configuration (AI_BASE_URL / AI_API_KEY /
	// AI_MODEL_*): any OpenAI-compatible endpoint. Unset → every op answers
	// AI_UNAVAILABLE instead of the service refusing to boot.
	client := llm.New(llm.ConfigFromEnv())

	r := gin.New()
	r.Use(response.GinRecovery())
	r.Use(telemetry.GinTraceMiddleware("ai-service"))
	r.Use(metrics.GinMetricsMiddleware())
	r.Use(logger.GinRequestID())
	r.Use(logger.GinRequestLogger())
	// Registered before the auth middleware so Prometheus can scrape without a
	// token — Gin binds a route's chain at registration, so a later r.Use does
	// not apply retroactively.
	r.GET("/metrics", metrics.MetricsHandler())
	if err := r.SetTrustedProxies(config.TrustedProxies()); err != nil {
		slog.Error("failed to set trusted proxies", "error", err)
		os.Exit(1)
	}
	r.Use(buildAuthMiddleware(denylist))

	routes.SetupRouter(r, &handlers.Ops{
		LLM:       client,
		Publish:   publish,
		Quota:     &quota.Ledger{DB: models.DB, Limits: quota.LimitsFromEnv()},
		Docs:      &doccache.Redis{Client: redisstore.Client, TTL: docCacheTTL()},
		MaxTokens: config.GetEnvInt("AI_MAX_OUTPUT_TOKENS", 4096),
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8092"
	}

	srv := &http.Server{Addr: ":" + port, Handler: r}
	// streaming=true: op answers stream as SSE, so WriteTimeout stays unset.
	config.ApplyServerTimeouts(srv, true)

	go func() {
		slog.Info("ai-service listening", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Info("shutting down ai-service...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("server forced to shutdown", "error", err)
	}
	slog.Info("ai-service exited")
}

// buildAuthMiddleware assembles this service's auth middleware.
//
// AUTH_TRUST_GATEWAY_HEADERS defaults to FALSE: trusting injected identity
// headers is only sound when nothing but the gateway can reach this service,
// and here a forged X-User-ID would spend another user's AI quota. With it
// off, the JWT is verified locally regardless of what the gateway claims.
//
// The middleware resolves identity but does not gate; RequireUser gates /api
// so /healthz and /readyz stay credential-less.
//
// NOTE: intentionally duplicated per service rather than shared (CLAUDE.md §1);
// notification-service/main.go and job-service/main.go carry the same function.
func buildAuthMiddleware(denylist authverify.TokenDenylist) gin.HandlerFunc {
	trustGateway := config.GetEnvBool("AUTH_TRUST_GATEWAY_HEADERS", false)

	verifier, err := authverify.NewVerifierFromEnv(denylist)
	if err != nil {
		slog.Error("auth verifier init failed", "error", err)
		os.Exit(1)
	}

	return authverify.GinAuthMiddleware(authverify.GinMiddlewareOptions{
		Verifier:            verifier,
		TrustGatewayHeaders: trustGateway,
	})
}

// docCacheTTL reads AI_DOC_CACHE_TTL (default 30m): long enough for a reading
// session's follow-up questions, short enough that page text does not linger.
func docCacheTTL() time.Duration {
	d, err := time.ParseDuration(config.GetEnv("AI_DOC_CACHE_TTL", "30m"))
	if err != nil || d <= 0 {
		return 30 * time.Minute
	}
	return d
}

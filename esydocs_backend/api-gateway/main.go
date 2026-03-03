package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"api-gateway/internal/authverify"
	"esydocs/shared/logger"
	"esydocs/shared/metrics"
	"esydocs/shared/telemetry"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

type routeConfig struct {
	prefix         string
	targetBasePath string
	targetURL      string
}

func validateJWTSecret() error {
	secret := os.Getenv("JWT_HS256_SECRET")
	if secret == "" {
		secret = os.Getenv("JWT_SECRET")
	}

	secret = strings.TrimSpace(secret)
	if secret == "" {
		return fmt.Errorf("JWT_HS256_SECRET environment variable is required but not set")
	}

	if len(secret) < 32 {
		return fmt.Errorf("JWT_HS256_SECRET must be at least 32 characters (256 bits), got %d characters", len(secret))
	}

	dangerousSecrets := []string{
		"4de0ea7311594deb860f03e5da60ac903fc4b4099bfe499a82e0fed013af32ca791ac065ea5e4d8aaade24a760e6dc58",
		"change-me",
		"secret",
		"password",
	}
	for _, dangerous := range dangerousSecrets {
		if secret == dangerous {
			return fmt.Errorf("JWT_HS256_SECRET appears to be a default/example value - use a cryptographically random secret")
		}
	}

	slog.Info("JWT secret validation passed")
	return nil
}

func main() {
	loadEnv()
	logger.Init("api-gateway", os.Getenv("LOG_MODE"))
	shutdownTracer := telemetry.Init("api-gateway")
	defer shutdownTracer(context.Background())

	if err := validateJWTSecret(); err != nil {
		slog.Error("JWT secret validation failed", "error", err)
		os.Exit(1)
	}

	port := getEnv("PORT", "8080")
	corsOrigins := parseCommaList(getEnv("CORS_ALLOW_ORIGINS", "http://localhost:5173"))
	corsMethods := getEnv("CORS_ALLOW_METHODS", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
	corsHeaders := getEnv("CORS_ALLOW_HEADERS", "Authorization,Content-Type,X-User-ID,X-Guest-Token")
	corsAllowCredentials := getEnv("CORS_ALLOW_CREDENTIALS", "true")

	redisClient, err := authverify.NewRedisClientFromEnv()
	if err != nil {
		slog.Error("auth redis init failed", "error", err)
		os.Exit(1)
	}
	defer redisClient.Close()

	guestStore := authverify.NewRedisGuestStore(redisClient, authverify.GuestStoreConfig{
		KeyPrefix: os.Getenv("AUTH_GUEST_PREFIX"),
		KeySuffix: os.Getenv("AUTH_GUEST_SUFFIX"),
	})
	var denylist authverify.TokenDenylist
	if getEnvBool("AUTH_DENYLIST_ENABLED", true) {
		if d := authverify.NewRedisTokenDenylist(redisClient, os.Getenv("AUTH_DENYLIST_PREFIX")); d != nil {
			denylist = d
			slog.Info("Token denylist enabled")
		} else {
			slog.Warn("Token denylist enabled but Redis unavailable")
		}
	} else {
		slog.Warn("Token denylist disabled - logout will not revoke access tokens")
	}

	verifier, err := authverify.NewVerifierFromEnv(denylist)
	if err != nil {
		slog.Error("auth verifier init failed", "error", err)
		os.Exit(1)
	}

	// Job service owns all job CRUD, uploads, and tool endpoints.
	// Auth routes go to auth-service when it exists, otherwise job-service.
	jobServiceURL := getEnv("JOB_SERVICE_URL", "http://job-service:8081")
	authServiceURL := getEnv("AUTH_SERVICE_URL", jobServiceURL) // Phase 2: separate auth-service

	routes := []routeConfig{
		{
			prefix:         "/auth",
			targetBasePath: "/auth",
			targetURL:      authServiceURL,
		},
		{
			prefix:         "/api/upload",
			targetBasePath: "/api/uploads",
			targetURL:      jobServiceURL,
		},
		{
			prefix:         "/api/convert-from-pdf",
			targetBasePath: "/api/convert-from-pdf",
			targetURL:      jobServiceURL,
		},
		{
			prefix:         "/api/convert-to-pdf",
			targetBasePath: "/api/convert-to-pdf",
			targetURL:      jobServiceURL,
		},
		{
			prefix:         "/api/organize-pdf",
			targetBasePath: "/api/organize-pdf",
			targetURL:      jobServiceURL,
		},
		{
			prefix:         "/api/optimize-pdf",
			targetBasePath: "/api/optimize-pdf",
			targetURL:      jobServiceURL,
		},
		{
			prefix:         "/api/jobs",
			targetBasePath: "/api/jobs",
			targetURL:      jobServiceURL,
		},
	}

	mux := http.NewServeMux()
	for _, cfg := range routes {
		mux.Handle(cfg.prefix, newProxy(cfg))
		mux.Handle(cfg.prefix+"/", newProxy(cfg))
	}

	mux.Handle("/metrics", metrics.HTTPMetricsHandler())

	// Fix #28: Deep health check - ping Redis
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := redisClient.Ping(ctx).Err(); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			resp, _ := json.Marshal(map[string]string{"status": "unhealthy", "redis": err.Error()})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(resp)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"healthy"}`))
	})

	slog.Info("api-gateway listening", "port", port)
	authMiddleware := authverify.HTTPAuthMiddleware(authverify.HTTPMiddlewareOptions{
		Verifier:   verifier,
		GuestStore: guestStore,
	})
	handler := telemetry.HTTPTraceMiddleware("api-gateway")(
		metrics.HTTPMetricsMiddleware(
			logger.HTTPRequestID(
				withCORS(authMiddleware(mux), corsConfig{
					allowedOrigins:   corsOrigins,
					allowedMethods:   corsMethods,
					allowedHeaders:   corsHeaders,
					allowCredentials: strings.EqualFold(corsAllowCredentials, "true"),
				}),
			),
		),
	)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func loadEnv() {
	candidates := []string{
		".env",
		filepath.Join("esydocs_backend", "api-gateway", ".env"),
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			if err := godotenv.Load(path); err != nil {
				slog.Error("failed to load env file", "path", path, "error", err)
			}
			normalizeEnv()
			return
		}
	}
	slog.Info("No .env file found, relying on environment variables")
	normalizeEnv()
}

func newProxy(cfg routeConfig) http.Handler {
	target, err := url.Parse(cfg.targetURL)
	if err != nil {
		slog.Error("invalid target URL", "prefix", cfg.prefix, "error", err)
		os.Exit(1)
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Director = func(req *http.Request) {
		targetPath := strings.TrimPrefix(req.URL.Path, cfg.prefix)
		if targetPath == "" {
			targetPath = "/"
		}
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.URL.Path = joinPath(cfg.targetBasePath, targetPath)
		req.Host = target.Host
		authverify.ClearUserHeaders(req.Header)
		if authCtx, ok := authverify.FromContext(req.Context()); ok {
			authverify.ApplyUserHeaders(req.Header, authCtx)
		}
	}

	return proxy
}

func joinPath(basePath, extraPath string) string {
	if basePath == "" {
		return extraPath
	}
	if strings.HasSuffix(basePath, "/") {
		return strings.TrimSuffix(basePath, "/") + extraPath
	}
	return basePath + extraPath
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func normalizeEnv() {
	for _, entry := range os.Environ() {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, value := parts[0], parts[1]
		cleaned := unquoteValue(value)
		if cleaned != value {
			_ = os.Setenv(key, cleaned)
		}
	}
}

func unquoteValue(value string) string {
	if len(value) < 2 {
		return value
	}
	if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
		return value[1 : len(value)-1]
	}
	return value
}

func getEnvBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "y":
		return true
	case "0", "false", "no", "n":
		return false
	default:
		return fallback
	}
}

type corsConfig struct {
	allowedOrigins   []string
	allowedMethods   string
	allowedHeaders   string
	allowCredentials bool
}

func withCORS(next http.Handler, cfg corsConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			allowOrigin := corsAllowOrigin(origin, cfg.allowedOrigins, cfg.allowCredentials)
			if allowOrigin != "" {
				w.Header().Set("Access-Control-Allow-Origin", allowOrigin)
				w.Header().Set("Vary", "Origin")
				if cfg.allowCredentials {
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}
				w.Header().Set("Access-Control-Allow-Methods", cfg.allowedMethods)
				w.Header().Set("Access-Control-Allow-Headers", cfg.allowedHeaders)
			}
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func corsAllowOrigin(origin string, allowedOrigins []string, allowCredentials bool) string {
	if origin == "" {
		return ""
	}
	for _, allowed := range allowedOrigins {
		if allowed == "*" {
			if allowCredentials {
				return origin
			}
			return "*"
		}
		if strings.EqualFold(strings.TrimSpace(allowed), origin) {
			return origin
		}
	}
	return ""
}

func parseCommaList(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	for i, part := range parts {
		parts[i] = strings.TrimSpace(part)
	}
	return parts
}

// redisClient is declared at package level for healthz, suppress unused import
var _ redis.Client

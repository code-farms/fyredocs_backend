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
	"os/signal"
	"strings"
	"syscall"
	"time"

	"api-gateway/internal/authverify"
	"api-gateway/internal/plancache"
	"esydocs/shared/config"
	"esydocs/shared/logger"
	"esydocs/shared/metrics"
	"esydocs/shared/telemetry"
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
	config.LoadConfig()
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
	corsHeaders := getEnv("CORS_ALLOW_HEADERS", "Authorization,Content-Type,X-User-ID")
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
	analyticsServiceURL := getEnv("ANALYTICS_SERVICE_URL", "http://analytics-service:8087")

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
		{
			prefix:         "/admin",
			targetBasePath: "/admin",
			targetURL:      analyticsServiceURL,
		},
	}

	mux := http.NewServeMux()
	for _, cfg := range routes {
		handler := newProxy(cfg)
		// Apply body size limit to non-upload routes (1MB for JSON endpoints).
		// Upload routes handle their own size limits.
		if !strings.HasPrefix(cfg.prefix, "/api/upload") {
			handler = withMaxBodySize(handler, 1<<20) // 1 MB
		}
		mux.Handle(cfg.prefix, handler)
		mux.Handle(cfg.prefix+"/", handler)
	}

	mux.Handle("/metrics", metrics.HTTPMetricsHandler())

	// SPA static file serving — serves the built frontend from the same origin
	// so that httpOnly cookies (guest_token, auth) work without cross-origin hacks.
	spaDir := getEnv("SPA_DIR", "")
	if spaDir != "" {
		if info, err := os.Stat(spaDir); err == nil && info.IsDir() {
			mux.Handle("/", spaFileServer(spaDir))
			slog.Info("SPA static files enabled", "dir", spaDir)
		} else {
			slog.Warn("SPA_DIR configured but not found, skipping static file serving", "dir", spaDir)
		}
	}

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

	// Warn about insecure CORS configuration
	credentialsEnabled := strings.EqualFold(corsAllowCredentials, "true")
	for _, origin := range corsOrigins {
		if origin == "*" && credentialsEnabled {
			slog.Warn("CORS_ALLOW_ORIGINS=* with CORS_ALLOW_CREDENTIALS=true effectively disables CORS protection — do not use in production")
			break
		}
	}

	authMiddleware := authverify.HTTPAuthMiddleware(authverify.HTTPMiddlewareOptions{
		Verifier:   verifier,
		GuestStore: guestStore,
		PublicPaths: []string{"/auth/login", "/auth/signup", "/auth/refresh", "/auth/plans"},
		ResolvePlan: func(r *http.Request, authCtx *authverify.AuthContext) {
			info := plancache.GetPlanInfo(r.Context(), redisClient, authCtx.UserID)
			authCtx.Plan = info.Plan
			authCtx.PlanMaxFileSizeMB = info.MaxFileMB
			authCtx.PlanMaxFilesPerJob = info.MaxFiles
		},
	})
	handler := telemetry.HTTPTraceMiddleware("api-gateway")(
		metrics.HTTPMetricsMiddleware(
			logger.HTTPRequestID(
				withSecurityHeaders(
					withCORS(authMiddleware(mux), corsConfig{
						allowedOrigins:   corsOrigins,
						allowedMethods:   corsMethods,
						allowedHeaders:   corsHeaders,
						allowCredentials: credentialsEnabled,
					}),
				),
			),
		),
	)

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: handler,
	}

	go func() {
		slog.Info("api-gateway listening", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Info("shutting down api-gateway...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("server forced to shutdown", "error", err)
	}
	redisClient.Close()
	slog.Info("api-gateway exited")
}

// proxyTransport is shared across all reverse proxies with sensible timeouts.
var proxyTransport = &http.Transport{
	ResponseHeaderTimeout: 5 * time.Minute, // allow long PDF conversions
	IdleConnTimeout:       90 * time.Second,
	MaxIdleConnsPerHost:   20,
	MaxIdleConns:          100,
}

func newProxy(cfg routeConfig) http.Handler {
	target, err := url.Parse(cfg.targetURL)
	if err != nil {
		slog.Error("invalid target URL", "prefix", cfg.prefix, "error", err)
		os.Exit(1)
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = proxyTransport
	proxy.FlushInterval = -1 // stream responses immediately (critical for file downloads)
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

// withMaxBodySize limits request body size for non-upload routes.
func withMaxBodySize(next http.Handler, maxBytes int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		next.ServeHTTP(w, r)
	})
}

// withSecurityHeaders adds standard security headers to every response.
func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

// spaFileServer serves static files from dir and falls back to index.html
// for any path that doesn't match a real file (SPA client-side routing).
func spaFileServer(dir string) http.Handler {
	fs := http.Dir(dir)
	fileServer := http.FileServer(fs)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}
		f, err := fs.Open(path)
		if err != nil {
			// File not found — serve index.html for SPA client-side routing
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}
		f.Close()

		// Cache static assets (hashed filenames) aggressively
		if strings.HasPrefix(path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}

		fileServer.ServeHTTP(w, r)
	})
}


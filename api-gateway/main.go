package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"api-gateway/internal/plancache"
	"api-gateway/internal/ratelimit"
	"fyredocs/shared/authverify"
	"fyredocs/shared/config"
	"fyredocs/shared/logger"
	"fyredocs/shared/metrics"
	"fyredocs/shared/telemetry"
)

type routeConfig struct {
	prefix         string
	targetBasePath string
	targetURL      string
}

func main() {
	config.LoadConfig()
	logger.Init("api-gateway", os.Getenv("LOG_MODE"))
	shutdownTracer := telemetry.Init("api-gateway")
	defer shutdownTracer(context.Background())

	if err := config.ValidateJWTSecret(); err != nil {
		slog.Error("JWT secret validation failed", "error", err)
		os.Exit(1)
	}

	port := config.GetEnv("PORT", "8080")
	corsOrigins := parseCommaList(config.GetEnv("CORS_ALLOW_ORIGINS", "http://localhost:5173"))
	corsMethods := config.GetEnv("CORS_ALLOW_METHODS", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
	corsHeaders := config.GetEnv("CORS_ALLOW_HEADERS", "Authorization,Content-Type,X-User-ID")
	corsAllowCredentials := config.GetEnv("CORS_ALLOW_CREDENTIALS", "true")

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
	if config.GetEnvBool("AUTH_DENYLIST_ENABLED", true) {
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
	jobServiceURL := config.GetEnv("JOB_SERVICE_URL", "http://job-service:8081")
	authServiceURL := config.GetEnv("AUTH_SERVICE_URL", jobServiceURL) // Phase 2: separate auth-service
	analyticsServiceURL := config.GetEnv("ANALYTICS_SERVICE_URL", "http://analytics-service:8087")
	documentServiceURL := config.GetEnv("DOCUMENT_SERVICE_URL", "http://document-service:8089")
	userServiceURL := config.GetEnv("USER_SERVICE_URL", "http://user-service:8090")
	notificationServiceURL := config.GetEnv("NOTIFICATION_SERVICE_URL", "http://notification-service:8091")

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
			prefix:         "/api/documents",
			targetBasePath: "/api/documents",
			targetURL:      documentServiceURL,
		},
		{
			prefix:         "/api/folders",
			targetBasePath: "/api/folders",
			targetURL:      documentServiceURL,
		},
		{
			prefix:         "/api/tags",
			targetBasePath: "/api/tags",
			targetURL:      documentServiceURL,
		},
		{
			prefix:         "/api/exports",
			targetBasePath: "/api/exports",
			targetURL:      documentServiceURL,
		},
		{
			prefix:         "/api/orgs",
			targetBasePath: "/api/orgs",
			targetURL:      userServiceURL,
		},
		{
			prefix:         "/api/notifications",
			targetBasePath: "/api/notifications",
			targetURL:      notificationServiceURL,
		},
		{
			prefix:         "/admin",
			targetBasePath: "/admin",
			targetURL:      analyticsServiceURL,
		},
		{
			prefix:         "/api/dashboard",
			targetBasePath: "/api/dashboard",
			targetURL:      analyticsServiceURL,
		},
	}

	mux := http.NewServeMux()
	registerServiceRoutes(mux, routes)

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

	// Warn about insecure CORS configuration
	credentialsEnabled := strings.EqualFold(corsAllowCredentials, "true")
	for _, origin := range corsOrigins {
		if origin == "*" && credentialsEnabled {
			slog.Warn("CORS_ALLOW_ORIGINS=* with CORS_ALLOW_CREDENTIALS=true effectively disables CORS protection — do not use in production")
			break
		}
	}

	authMiddleware := authverify.HTTPAuthMiddleware(authverify.HTTPMiddlewareOptions{
		Verifier:    verifier,
		GuestStore:  guestStore,
		PublicPaths: []string{"/auth/login", "/auth/signup", "/auth/refresh", "/auth/plans"},
		ResolvePlan: func(r *http.Request, authCtx *authverify.AuthContext) {
			info := plancache.GetPlanInfo(r.Context(), redisClient, authCtx.UserID)
			authCtx.Plan = info.Plan
			authCtx.PlanMaxFileSizeMB = info.MaxFileMB
			authCtx.PlanMaxFilesPerJob = info.MaxFiles
		},
	})

	// Per-plan sliding-window rate limiting on /api/* routes. Runs inside the
	// auth middleware so the resolved AuthContext (user ID + plan) is available
	// for keying; fails open on Redis errors.
	rateLimit := ratelimit.Middleware(ratelimit.Config{
		Client:     redisClient,
		Window:     config.GetEnvDuration("RATE_LIMIT_API_WINDOW", time.Minute),
		GuestLimit: config.GetEnvInt("RATE_LIMIT_API_GUEST", 30),
		FreeLimit:  config.GetEnvInt("RATE_LIMIT_API_FREE", 120),
		ProLimit:   config.GetEnvInt("RATE_LIMIT_API_PRO", 600),
	})

	// SPA static files and presigned MinIO byte traffic are handled by the
	// Caddy edge (deployment/caddy/Caddyfile), not this gateway — file
	// bandwidth must never compete with API routing for gateway CPU.
	root := withCORS(authMiddleware(rateLimit(mux)), corsConfig{
		allowedOrigins:   corsOrigins,
		allowedMethods:   corsMethods,
		allowedHeaders:   corsHeaders,
		allowCredentials: credentialsEnabled,
	})

	handler := telemetry.HTTPTraceMiddleware("api-gateway")(
		metrics.HTTPMetricsMiddleware(
			logger.HTTPRequestID(
				withSecurityHeaders(root),
			),
		),
	)

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: handler,
	}
	// streaming=true: the gateway proxies long file downloads and SSE streams,
	// so WriteTimeout must stay unset. Header/read/idle timeouts still apply.
	config.ApplyServerTimeouts(srv, true)

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

// registerServiceRoutes mounts each backend route with the standard 1 MiB
// JSON body limit. File bytes never pass through this gateway — uploads and
// downloads go directly from the browser to MinIO via presigned URLs routed
// at the Caddy edge — so /api/upload is JSON-only (init/complete) and gets
// the same limit.
func registerServiceRoutes(mux *http.ServeMux, routes []routeConfig) {
	for _, cfg := range routes {
		handler := withMaxBodySize(newProxy(cfg), 1<<20) // 1 MiB
		mux.Handle(cfg.prefix, handler)
		mux.Handle(cfg.prefix+"/", handler)
	}
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
		// Strip the gateway prefix and re-root the request under the upstream's
		// base path. An exact-prefix match (e.g. GET /api/dashboard) yields an
		// empty remainder; joinPath then forwards targetBasePath verbatim. We
		// must NOT default the remainder to "/" here — that would append a
		// trailing slash (/api/dashboard/), which upstream Gin routers answer
		// with a 301 redirect, making the browser fetch loop on the same path.
		targetPath := strings.TrimPrefix(req.URL.Path, cfg.prefix)
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

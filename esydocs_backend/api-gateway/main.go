package main

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"api-gateway/auth"
	"github.com/joho/godotenv"
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

	// Minimum entropy check: 32 bytes (256 bits) for HS256
	if len(secret) < 32 {
		return fmt.Errorf("JWT_HS256_SECRET must be at least 32 characters (256 bits), got %d characters", len(secret))
	}

	// Check if it's the example/default secret (security smell)
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

	log.Println("JWT secret validation passed")
	return nil
}

func main() {
	loadEnv()

	// SECURITY: Validate JWT secret is set and meets minimum requirements
	if err := validateJWTSecret(); err != nil {
		log.Fatalf("JWT secret validation failed: %v\n\nFor local development:\n  1. Copy .env.example to .env\n  2. Generate a secret: openssl rand -hex 32\n  3. Set JWT_HS256_SECRET in .env\n\nFor production:\n  Set environment variable: export JWT_HS256_SECRET=\"your-secret-here\"", err)
	}

	port := getEnv("PORT", "8080")
	corsOrigins := parseCommaList(getEnv("CORS_ALLOW_ORIGINS", "http://localhost:5173"))
	corsMethods := getEnv("CORS_ALLOW_METHODS", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
	corsHeaders := getEnv("CORS_ALLOW_HEADERS", "Authorization,Content-Type,X-User-ID,X-Guest-Token")
	corsAllowCredentials := getEnv("CORS_ALLOW_CREDENTIALS", "true")

	redisClient, err := auth.NewRedisClientFromEnv()
	if err != nil {
		log.Fatalf("auth redis init failed: %v", err)
	}
	defer redisClient.Close()

	guestStore := auth.NewRedisGuestStore(redisClient, auth.GuestStoreConfig{
		KeyPrefix: os.Getenv("AUTH_GUEST_PREFIX"),
		KeySuffix: os.Getenv("AUTH_GUEST_SUFFIX"),
	})
	var denylist auth.TokenDenylist
	if getEnvBool("AUTH_DENYLIST_ENABLED", true) {
		if d := auth.NewRedisTokenDenylist(redisClient, os.Getenv("AUTH_DENYLIST_PREFIX")); d != nil {
			denylist = d
			log.Println("Token denylist enabled")
		} else {
			log.Println("WARNING: Token denylist enabled but Redis unavailable")
		}
	} else {
		log.Println("WARNING: Token denylist disabled - logout will not revoke access tokens")
	}

	verifier, err := auth.NewVerifierFromEnv(denylist)
	if err != nil {
		log.Fatalf("auth verifier init failed: %v", err)
	}

	uploadURL := getEnv("UPLOAD_SERVICE_URL", "http://upload-service:8081")
	convertFromURL := getEnv("CONVERT_FROM_PDF_URL", uploadURL)
	convertToURL := getEnv("CONVERT_TO_PDF_URL", uploadURL)
	organizeURL := getEnv("ORGANIZE_PDF_URL", uploadURL)
	optimizeURL := getEnv("OPTIMIZE_PDF_URL", uploadURL)

	routes := []routeConfig{
		{
			prefix:         "/auth",
			targetBasePath: "/auth",
			targetURL:      uploadURL,
		},
		{
			prefix:         "/api/upload",
			targetBasePath: "/api/uploads",
			targetURL:      uploadURL,
		},
		{
			prefix:         "/api/convert-from-pdf",
			targetBasePath: "/api/convert-from-pdf",
			targetURL:      convertFromURL,
		},
		{
			prefix:         "/api/convert-to-pdf",
			targetBasePath: "/api/convert-to-pdf",
			targetURL:      convertToURL,
		},
		{
			prefix:         "/api/organize-pdf",
			targetBasePath: "/api/organize-pdf",
			targetURL:      organizeURL,
		},
		{
			prefix:         "/api/optimize-pdf",
			targetBasePath: "/api/optimize-pdf",
			targetURL:      optimizeURL,
		},
		{
			prefix:         "/api/jobs",
			targetBasePath: "/api/jobs",
			targetURL:      uploadURL,
		},
	}

	mux := http.NewServeMux()
	for _, cfg := range routes {
		mux.Handle(cfg.prefix, newProxy(cfg))
		mux.Handle(cfg.prefix+"/", newProxy(cfg))
	}

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("api-gateway listening on :%s", port)
	authMiddleware := auth.HTTPAuthMiddleware(auth.HTTPMiddlewareOptions{
		Verifier:   verifier,
		GuestStore: guestStore,
	})
	handler := withCORS(authMiddleware(mux), corsConfig{
		allowedOrigins:    corsOrigins,
		allowedMethods:    corsMethods,
		allowedHeaders:    corsHeaders,
		allowCredentials:  strings.EqualFold(corsAllowCredentials, "true"),
	})
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatalf("server failed: %v", err)
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
				log.Printf("Failed to load env file %s: %v", path, err)
			}
			normalizeEnv()
			return
		}
	}
	log.Println("No .env file found, relying on environment variables")
	normalizeEnv()
}

func newProxy(cfg routeConfig) http.Handler {
	target, err := url.Parse(cfg.targetURL)
	if err != nil {
		log.Fatalf("invalid target URL for %s: %v", cfg.prefix, err)
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
		auth.ClearUserHeaders(req.Header)
		if authCtx, ok := auth.FromContext(req.Context()); ok {
			auth.ApplyUserHeaders(req.Header, authCtx)
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

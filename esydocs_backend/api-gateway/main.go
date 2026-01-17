package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"

	"api-gateway/auth"
)

type routeConfig struct {
	prefix         string
	targetBasePath string
	targetURL      string
}

func main() {
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
	if getEnvBool("AUTH_DENYLIST_ENABLED", false) {
		denylist = auth.NewRedisTokenDenylist(redisClient, os.Getenv("AUTH_DENYLIST_PREFIX"))
	}

	verifier, err := auth.NewVerifierFromEnv(denylist)
	if err != nil {
		log.Fatalf("auth verifier init failed: %v", err)
	}

	uploadURL := getEnv("UPLOAD_SERVICE_URL", "http://upload-service:8081")
	convertFromURL := getEnv("CONVERT_FROM_PDF_URL", uploadURL)
	convertToURL := getEnv("CONVERT_TO_PDF_URL", uploadURL)

	routes := []routeConfig{
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

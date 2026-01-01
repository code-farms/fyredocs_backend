package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
)

type routeConfig struct {
	prefix         string
	targetBasePath string
	targetURL      string
}

func main() {
	port := getEnv("PORT", "8080")

	uploadURL := getEnv("UPLOAD_SERVICE_URL", "http://upload-service:8081")
	convertFromURL := getEnv("CONVERT_FROM_PDF_URL", "http://convert-from-pdf:8082")
	convertToURL := getEnv("CONVERT_TO_PDF_URL", "http://convert-to-pdf:8083")

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
	if err := http.ListenAndServe(":"+port, mux); err != nil {
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

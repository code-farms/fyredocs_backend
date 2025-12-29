package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
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
			targetBasePath: "/api",
			targetURL:      convertFromURL,
		},
		{
			prefix:         "/api/convert-to-pdf",
			targetBasePath: "/api",
			targetURL:      convertToURL,
		},
	}

	mux := http.NewServeMux()
	for _, cfg := range routes {
		mux.Handle(cfg.prefix, newProxy(cfg))
		mux.Handle(cfg.prefix+"/", newProxy(cfg))
	}

	mux.HandleFunc("/api/jobs", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			merged, err := aggregateJobs(convertFromURL, convertToURL)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			writeJSON(w, merged)
		case http.MethodPost:
			body, err := readBody(r)
			if err != nil {
				http.Error(w, "failed to read request body", http.StatusBadRequest)
				return
			}
			toolType, err := extractToolType(r, body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			target := resolveServiceURL(toolType, convertFromURL, convertToURL)
			forwardRequest(w, r, target+"/api/jobs", body)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/jobs/", func(w http.ResponseWriter, r *http.Request) {
		body, err := readBody(r)
		if err != nil {
			http.Error(w, "failed to read request body", http.StatusBadRequest)
			return
		}
		path := r.URL.Path
		if !tryForward(w, r, convertFromURL+path, body) {
			if !tryForward(w, r, convertToURL+path, body) {
				http.Error(w, "not found", http.StatusNotFound)
			}
		}
	})

	mux.HandleFunc("/api/download/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if !tryForward(w, r, convertFromURL+path, nil) {
			if !tryForward(w, r, convertToURL+path, nil) {
				http.Error(w, "not found", http.StatusNotFound)
			}
		}
	})

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

func aggregateJobs(convertFromURL, convertToURL string) ([]json.RawMessage, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	urls := []string{convertFromURL + "/api/jobs", convertToURL + "/api/jobs"}
	var combined []json.RawMessage
	for _, endpoint := range urls {
		resp, err := client.Get(endpoint)
		if err != nil {
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			_ = resp.Body.Close()
			continue
		}
		var items []json.RawMessage
		if err := json.NewDecoder(resp.Body).Decode(&items); err == nil {
			combined = append(combined, items...)
		}
		_ = resp.Body.Close()
	}
	return combined, nil
}

func writeJSON(w http.ResponseWriter, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	defer r.Body.Close()
	return io.ReadAll(r.Body)
}

func extractToolType(r *http.Request, body []byte) (string, error) {
	contentType := r.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err == nil && strings.HasPrefix(mediaType, "multipart/form-data") {
		boundary := params["boundary"]
		if boundary == "" {
			return "", errors.New("missing multipart boundary")
		}
		reader := multipart.NewReader(bytes.NewReader(body), boundary)
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", errors.New("invalid multipart payload")
			}
			if part.FormName() == "toolType" {
				value, _ := io.ReadAll(part)
				return normalizeToolType(strings.TrimSpace(string(value))), nil
			}
		}
	}

	if strings.Contains(contentType, "application/json") {
		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			return "", errors.New("invalid json payload")
		}
		if tool, ok := payload["toolType"].(string); ok && tool != "" {
			return normalizeToolType(strings.TrimSpace(tool)), nil
		}
	}

	return "", errors.New("toolType is required")
}

func normalizeToolType(toolType string) string {
	if toolType == "powerpoint-to-pdf" {
		return "ppt-to-pdf"
	}
	return toolType
}

func resolveServiceURL(toolType, convertFromURL, convertToURL string) string {
	if isConvertToPdfTool(toolType) {
		return convertToURL
	}
	return convertFromURL
}

func isConvertToPdfTool(toolType string) bool {
	switch toolType {
	case "word-to-pdf", "ppt-to-pdf", "excel-to-pdf", "image-to-pdf", "img-to-pdf":
		return true
	default:
		return false
	}
}

func tryForward(w http.ResponseWriter, r *http.Request, targetURL string, body []byte) bool {
	resp, err := doRequest(r, targetURL, body)
	if err != nil {
		http.Error(w, "upstream error", http.StatusBadGateway)
		return true
	}
	if resp == nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, resp.Body)
		return false
	}
	writeResponse(w, resp)
	return true
}

func forwardRequest(w http.ResponseWriter, r *http.Request, targetURL string, body []byte) (*http.Response, error) {
	resp, err := doRequest(r, targetURL, body)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, nil
	}
	defer resp.Body.Close()
	writeResponse(w, resp)
	return resp, nil
}

func doRequest(r *http.Request, targetURL string, body []byte) (*http.Response, error) {
	reqBody := body
	if reqBody == nil && r.Body != nil {
		var err error
		reqBody, err = readBody(r)
		if err != nil {
			return nil, err
		}
	}

	request, err := http.NewRequest(r.Method, targetURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	request.URL.RawQuery = r.URL.RawQuery
	request.Header = r.Header.Clone()

	client := &http.Client{Timeout: 60 * time.Second}
	return client.Do(request)
}

func writeResponse(w http.ResponseWriter, resp *http.Response) {
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
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

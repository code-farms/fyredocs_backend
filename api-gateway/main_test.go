package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"testing"
)

func TestCorsAllowOrigin(t *testing.T) {
	tests := []struct {
		name             string
		origin           string
		allowed          []string
		allowCredentials bool
		want             string
	}{
		{"empty origin", "", []string{"http://example.com"}, false, ""},
		{"wildcard without credentials", "http://any.com", []string{"*"}, false, "*"},
		{"wildcard with credentials returns origin", "http://any.com", []string{"*"}, true, "http://any.com"},
		{"matching origin", "http://localhost:5173", []string{"http://localhost:5173"}, false, "http://localhost:5173"},
		{"non-matching origin", "http://evil.com", []string{"http://localhost:5173"}, false, ""},
		{"case insensitive match", "HTTP://LOCALHOST:5173", []string{"http://localhost:5173"}, false, "HTTP://LOCALHOST:5173"},
		{"multiple allowed origins", "http://example.com", []string{"http://localhost:5173", "http://example.com"}, false, "http://example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := corsAllowOrigin(tt.origin, tt.allowed, tt.allowCredentials)
			if got != tt.want {
				t.Errorf("corsAllowOrigin(%q, %v, %v) = %q, want %q", tt.origin, tt.allowed, tt.allowCredentials, got, tt.want)
			}
		})
	}
}

func TestJoinPath(t *testing.T) {
	tests := []struct {
		basePath  string
		extraPath string
		want      string
	}{
		{"", "/hello", "/hello"},
		{"/api", "/users", "/api/users"},
		{"/api/", "/users", "/api/users"},
		{"/api", "", "/api"},
	}

	for _, tt := range tests {
		got := joinPath(tt.basePath, tt.extraPath)
		if got != tt.want {
			t.Errorf("joinPath(%q, %q) = %q, want %q", tt.basePath, tt.extraPath, got, tt.want)
		}
	}
}

func TestParseCommaList(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"http://localhost:5173", 1},
		{"http://a.com, http://b.com, http://c.com", 3},
	}

	for _, tt := range tests {
		got := parseCommaList(tt.input)
		if tt.input == "" && got != nil {
			t.Errorf("parseCommaList(%q) = %v, want nil", tt.input, got)
			continue
		}
		if tt.input != "" && len(got) != tt.want {
			t.Errorf("parseCommaList(%q) len = %d, want %d", tt.input, len(got), tt.want)
		}
	}
}

func TestWithCORSPreflight(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := withCORS(next, corsConfig{
		allowedOrigins:   []string{"http://localhost:5173"},
		allowedMethods:   "GET,POST",
		allowedHeaders:   "Content-Type",
		allowCredentials: true,
	})

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected %d for preflight, got %d", http.StatusNoContent, rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Errorf("expected origin header 'http://localhost:5173', got %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("expected credentials header 'true', got %q", got)
	}
}

func TestWithSecurityHeaders(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := withSecurityHeaders(next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	expectedHeaders := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":       "DENY",
		"X-XSS-Protection":      "1; mode=block",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
		"Permissions-Policy":     "camera=(), microphone=(), geolocation=()",
	}
	for key, want := range expectedHeaders {
		got := rec.Header().Get(key)
		if got != want {
			t.Errorf("header %s = %q, want %q", key, got, want)
		}
	}
}

func TestWithMaxBodySize(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to read the body — if over limit, Read returns an error
		buf := make([]byte, 1024)
		_, err := r.Body.Read(buf)
		if err != nil && err.Error() == "http: request body too large" {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	t.Run("small body passes", func(t *testing.T) {
		handler := withMaxBodySize(next, 1<<20) // 1MB
		body := make([]byte, 100)
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 for small body, got %d", rec.Code)
		}
	})

	t.Run("oversized body rejected", func(t *testing.T) {
		handler := withMaxBodySize(next, 100) // 100 bytes limit
		body := make([]byte, 200)
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("expected 413 for oversized body, got %d", rec.Code)
		}
	})
}

func TestWithCORSNonMatchingOrigin(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := withCORS(next, corsConfig{
		allowedOrigins: []string{"http://localhost:5173"},
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "http://evil.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no CORS header for non-matching origin, got %q", got)
	}
}

func TestNewProxyStreamsResponses(t *testing.T) {
	// Verify the proxy is configured to flush immediately (no buffering),
	// which is critical for file download performance.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	handler := newProxy(routeConfig{
		targetURL:      backend.URL,
		prefix:         "/test",
		targetBasePath: "",
	})

	// newProxy returns an *httputil.ReverseProxy wrapped as http.Handler.
	proxy, ok := handler.(*httputil.ReverseProxy)
	if !ok {
		t.Fatal("newProxy did not return *httputil.ReverseProxy")
	}
	if proxy.FlushInterval != -1 {
		t.Errorf("proxy.FlushInterval = %v, want -1 (immediate flush for streaming)", proxy.FlushInterval)
	}
}

func TestWithCORSNoOriginHeader(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := withCORS(next, corsConfig{
		allowedOrigins: []string{"http://localhost:5173"},
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("expected next handler to be called")
	}
}

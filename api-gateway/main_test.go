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

func TestNewMinioProxy(t *testing.T) {
	type captured struct {
		host  string
		path  string
		query string
	}
	var got captured
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = captured{host: r.Host, path: r.URL.Path, query: r.URL.RawQuery}
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	proxy := newMinioProxy(backend.URL)

	t.Run("uploads bucket: path verbatim and Host preserved", func(t *testing.T) {
		got = captured{}
		req := httptest.NewRequest(http.MethodPut,
			"/fyredocs-uploads/uploads/u1/file.pdf?uploadId=abc&partNumber=1&X-Amz-Signature=sig", nil)
		req.Host = "localhost:8080" // the origin the URL was presigned for
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 from backend, got %d", rec.Code)
		}
		if got.path != "/fyredocs-uploads/uploads/u1/file.pdf" {
			t.Errorf("backend path = %q, want verbatim /fyredocs-uploads/... (no prefix strip)", got.path)
		}
		if got.query != "uploadId=abc&partNumber=1&X-Amz-Signature=sig" {
			t.Errorf("backend query = %q, want presigned query preserved", got.query)
		}
		if got.host != "localhost:8080" {
			t.Errorf("backend Host = %q, want original host localhost:8080 (SigV4 signs it)", got.host)
		}
	})

	t.Run("outputs bucket routes through same proxy", func(t *testing.T) {
		got = captured{}
		req := httptest.NewRequest(http.MethodGet,
			"/fyredocs-outputs/jobs/j1/converted.docx?X-Amz-Signature=sig", nil)
		req.Host = "app.example.com"
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, req)

		if got.path != "/fyredocs-outputs/jobs/j1/converted.docx" {
			t.Errorf("backend path = %q, want verbatim", got.path)
		}
		if got.host != "app.example.com" {
			t.Errorf("backend Host = %q, want app.example.com", got.host)
		}
	})

	t.Run("identity headers are stripped", func(t *testing.T) {
		var gotUserID string
		strip := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotUserID = r.Header.Get("X-User-ID")
			w.WriteHeader(http.StatusOK)
		}))
		defer strip.Close()

		p := newMinioProxy(strip.URL)
		req := httptest.NewRequest(http.MethodGet, "/fyredocs-outputs/jobs/j1/file.pdf", nil)
		req.Header.Set("X-User-ID", "spoofed")
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, req)

		if gotUserID != "" {
			t.Errorf("X-User-ID = %q forwarded to MinIO, want stripped", gotUserID)
		}
	})

	t.Run("streams immediately", func(t *testing.T) {
		proxy, ok := newMinioProxy(backend.URL).(*httputil.ReverseProxy)
		if !ok {
			t.Fatal("newMinioProxy did not return *httputil.ReverseProxy")
		}
		if proxy.FlushInterval != -1 {
			t.Errorf("FlushInterval = %v, want -1", proxy.FlushInterval)
		}
		transport, ok := proxy.Transport.(*http.Transport)
		if !ok {
			t.Fatal("minio proxy transport is not *http.Transport")
		}
		if transport.MaxIdleConnsPerHost != 50 {
			t.Errorf("MaxIdleConnsPerHost = %d, want 50 (parallel multipart parts)", transport.MaxIdleConnsPerHost)
		}
	})
}

func TestRegisterServiceRoutesAppliesBodyLimitToUploadRoute(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drain the body like a real JSON handler would.
		buf := make([]byte, 32<<10)
		for {
			if _, err := r.Body.Read(buf); err != nil {
				break
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	mux := http.NewServeMux()
	registerServiceRoutes(mux, []routeConfig{
		{prefix: "/api/upload", targetBasePath: "/api/uploads", targetURL: backend.URL},
	})

	t.Run("small JSON body passes", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/upload/init", bytes.NewReader(make([]byte, 512)))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 for small body, got %d", rec.Code)
		}
	})

	t.Run("body over 1 MiB is rejected (upload exemption removed)", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/upload/init", bytes.NewReader(make([]byte, 2<<20)))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		// MaxBytesReader aborts the proxied request mid-body; depending on
		// where the read fails the proxy surfaces 413 or 502 — never 2xx.
		if rec.Code < 400 {
			t.Errorf("expected error status for oversized body on /api/upload, got %d", rec.Code)
		}
	})
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

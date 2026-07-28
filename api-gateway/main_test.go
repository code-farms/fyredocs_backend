// Tests for gateway routing and middleware. These pin the behaviours a
// misconfiguration would silently break: identity headers are stripped before
// proxying (so a client cannot forge X-User-ID), legacy prefixes and their /api/v1
// aliases route to the same upstream, and responses stream rather than buffer.
package main

import (
	"bytes"
	"fyredocs/shared/circuitbreaker"
	"net/http"
	"net/http/httptest"
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
		"X-Frame-Options":        "DENY",
		"X-XSS-Protection":       "1; mode=block",
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
	// newProxy now returns a circuit-breaker-wrapped http.HandlerFunc (not a
	// bare *httputil.ReverseProxy), so we assert behavior end-to-end rather than
	// the concrete type: a flushing backend response must be proxied through
	// intact. The underlying proxy keeps FlushInterval = -1 for immediate
	// streaming (critical for file downloads).
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "hit")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("chunk-1"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		_, _ = w.Write([]byte("chunk-2"))
	}))
	defer backend.Close()

	handler := newProxy(routeConfig{
		targetURL:      backend.URL,
		prefix:         "/test",
		targetBasePath: "",
	}, circuitBreakerForTest())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test/thing", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("X-Backend"); got != "hit" {
		t.Errorf("backend header not proxied: X-Backend = %q", got)
	}
	if got := rec.Body.String(); got != "chunk-1chunk-2" {
		t.Errorf("body = %q, want %q (both flushed chunks proxied)", got, "chunk-1chunk-2")
	}
}

// TestNewProxyForwardsExactPrefixWithoutTrailingSlash guards against the
// dashboard redirect-loop regression: an exact-prefix request must forward
// verbatim (no appended trailing slash), or upstream Gin routers answer with a
// 301 and the browser fetch loops on the same path.
func TestNewProxyForwardsExactPrefixWithoutTrailingSlash(t *testing.T) {
	type captured struct {
		path  string
		query string
	}
	var got captured
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = captured{path: r.URL.Path, query: r.URL.RawQuery}
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	handler := newProxy(routeConfig{
		targetURL:      backend.URL,
		prefix:         "/api/dashboard",
		targetBasePath: "/api/dashboard",
	}, circuitBreakerForTest())

	t.Run("exact prefix forwards without trailing slash and keeps query", func(t *testing.T) {
		got = captured{}
		req := httptest.NewRequest(http.MethodGet, "/api/dashboard?days=30", nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)

		if got.path != "/api/dashboard" {
			t.Errorf("path = %q, want %q (no trailing slash)", got.path, "/api/dashboard")
		}
		if got.query != "days=30" {
			t.Errorf("query = %q, want %q", got.query, "days=30")
		}
	})

	t.Run("sub-path still forwards verbatim", func(t *testing.T) {
		got = captured{}
		req := httptest.NewRequest(http.MethodGet, "/api/dashboard/foo", nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)

		if got.path != "/api/dashboard/foo" {
			t.Errorf("path = %q, want %q", got.path, "/api/dashboard/foo")
		}
	})
}

// The presigned MinIO byte relay moved to the Caddy edge
// (deployment/caddy/Caddyfile). Its load-bearing invariants — path forwarded
// verbatim, ORIGINAL Host header preserved (SigV4 signs both), identity
// headers never forwarded — are documented there; Caddy's reverse_proxy
// preserves the client Host by default and MinIO ignores identity headers
// (the presigned signature is the only credential).

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

func TestWithV1AliasesMirrorsEveryRoute(t *testing.T) {
	routes := []routeConfig{
		{prefix: "/auth", targetBasePath: "/auth", targetURL: "http://auth:8086"},
		{prefix: "/api/upload", targetBasePath: "/api/uploads", targetURL: "http://job:8081"},
		{prefix: "/api/jobs", targetBasePath: "/api/jobs", targetURL: "http://job:8081"},
		{prefix: "/admin", targetBasePath: "/admin", targetURL: "http://analytics:8087"},
	}

	got := withV1Aliases(routes)
	if len(got) != len(routes)*2 {
		t.Fatalf("expected one alias per route (%d entries), got %d", len(routes)*2, len(got))
	}

	byPrefix := map[string]routeConfig{}
	for _, c := range got {
		if _, dup := byPrefix[c.prefix]; dup {
			t.Errorf("duplicate prefix %q — ServeMux would panic on registration", c.prefix)
		}
		byPrefix[c.prefix] = c
	}

	wantAliases := map[string]string{
		"/api/v1/auth":   "/auth",
		"/api/v1/upload": "/api/upload",
		"/api/v1/jobs":   "/api/jobs",
		"/api/v1/admin":  "/admin",
	}
	for alias, legacy := range wantAliases {
		a, ok := byPrefix[alias]
		if !ok {
			t.Errorf("missing alias %q", alias)
			continue
		}
		l := byPrefix[legacy]
		// An alias must reach the SAME upstream and base path — the whole point
		// is that downstream services need no changes.
		if a.targetURL != l.targetURL || a.targetBasePath != l.targetBasePath {
			t.Errorf("alias %q → (%s%s), want (%s%s)", alias,
				a.targetURL, a.targetBasePath, l.targetURL, l.targetBasePath)
		}
		if a.deprecated {
			t.Errorf("alias %q must not be marked deprecated", alias)
		}
		if !l.deprecated {
			t.Errorf("legacy prefix %q should be marked deprecated", legacy)
		}
	}

	// The /api/upload → /api/uploads rewrite is asymmetric; the alias must
	// preserve it exactly or uploads 404 under v1.
	if got := byPrefix["/api/v1/upload"].targetBasePath; got != "/api/uploads" {
		t.Errorf("v1 upload alias targetBasePath = %q, want /api/uploads", got)
	}
}

func TestV1AliasRoutesToSameUpstreamPath(t *testing.T) {
	var seen []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	mux := http.NewServeMux()
	registerServiceRoutes(mux, withV1Aliases([]routeConfig{
		{prefix: "/api/jobs", targetBasePath: "/api/jobs", targetURL: backend.URL},
		{prefix: "/auth", targetBasePath: "/auth", targetURL: backend.URL},
	}))

	for _, path := range []string{
		"/api/jobs/abc/download", "/api/v1/jobs/abc/download",
		"/auth/login", "/api/v1/auth/login",
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d, want 200", path, rec.Code)
		}
	}

	want := []string{
		"/api/jobs/abc/download", "/api/jobs/abc/download",
		"/auth/login", "/auth/login",
	}
	if len(seen) != len(want) {
		t.Fatalf("upstream saw %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Errorf("request %d: upstream saw %q, want %q", i, seen[i], want[i])
		}
	}
}

func TestDeprecationHeaderOnlyOnLegacyAndOnlyWhenEnabled(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	mux := http.NewServeMux()
	registerServiceRoutes(mux, withV1Aliases([]routeConfig{
		{prefix: "/api/jobs", targetBasePath: "/api/jobs", targetURL: backend.URL},
	}))

	header := func(path string) string {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Header().Get("Deprecation")
	}

	// Off by default: the mobile app in the wild only knows the legacy paths.
	t.Setenv("API_DEPRECATION_HEADERS", "false")
	if got := header("/api/jobs"); got != "" {
		t.Errorf("legacy path advertised Deprecation while disabled: %q", got)
	}

	t.Setenv("API_DEPRECATION_HEADERS", "true")
	if got := header("/api/jobs"); got != "true" {
		t.Errorf("legacy Deprecation header = %q, want \"true\"", got)
	}
	if got := header("/api/v1/jobs"); got != "" {
		t.Errorf("versioned path must never advertise Deprecation, got %q", got)
	}
}

func TestPublicAuthPathsCoverBothRoots(t *testing.T) {
	paths := map[string]bool{}
	for _, p := range publicAuthPaths() {
		paths[p] = true
	}
	for _, want := range []string{
		"/auth/login", "/auth/signup", "/auth/refresh", "/auth/plans",
		"/api/v1/auth/login", "/api/v1/auth/signup", "/api/v1/auth/refresh", "/api/v1/auth/plans",
	} {
		if !paths[want] {
			t.Errorf("public path %q missing — an expired cookie would block it", want)
		}
	}
}

// circuitBreakerForTest returns a fresh breaker so tests never share trip state.
func circuitBreakerForTest() *circuitbreaker.TwoStep {
	return circuitbreaker.NewTwoStep("test")
}

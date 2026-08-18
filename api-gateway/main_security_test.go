// Security pins for the gateway's authentication perimeter. These tests freeze
// two invariants a refactor could silently break: (1) exactly the intended set
// of paths bypasses token verification — in particular "/share/<token>" is
// public while "/api/share/..." and "/api/v1/share/..." are NOT (making those
// public would let anyone DELETE another user's share link, see the
// PublicPrefixes comment in main.go); and (2) client-supplied X-User-* identity
// headers never survive the proxy — ClearUserHeaders runs before
// ApplyUserHeaders in newProxy's director, so downstream services only ever see
// the gateway-verified identity.
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"fyredocs/shared/authverify"
)

// gatewayAuthMiddlewareForTest builds the REAL auth middleware with the same
// PublicPaths/PublicPrefixes wiring main.go uses (main.go:360-381), minus the
// Redis-backed guest store and plan resolver — neither affects the
// public-vs-protected decision under test. The verifier is a working HS256
// verifier so a stale cookie is actually verified (and rejected) rather than
// short-circuited by a nil Verifier.
func gatewayAuthMiddlewareForTest(t *testing.T) func(http.Handler) http.Handler {
	t.Helper()
	v, err := authverify.NewVerifier(authverify.VerifierConfig{
		AllowedAlgs: []string{"HS256"},
		HMACSecret:  []byte("test-secret-key-32-chars-long!!"),
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return authverify.HTTPAuthMiddleware(authverify.HTTPMiddlewareOptions{
		Verifier:       v,
		PublicPaths:    publicAuthPaths(),
		PublicPrefixes: []string{"/share/"},
	})
}

// TestGatewayAuthPublicPathMatrix pins which paths bypass token verification.
// The stale-cookie rows are the load-bearing ones: a public path must serve a
// caller whose access_token cookie is expired garbage (else an expired session
// locks the user out of logging back in), while every protected path must 401
// that same caller instead of falling through as a guest.
//
// The forgot/reset-password rows are regression rows: both paths were once
// absent from publicAuthPaths() even though auth-service registers them as
// public (auth-service/routes/routes.go), so a caller holding a stale
// access_token cookie got 401 on exactly the endpoints a locked-out user
// needs — the deadlock publicAuthPaths exists to prevent.
func TestGatewayAuthPublicPathMatrix(t *testing.T) {
	var gotCtx authverify.AuthContext
	var nextCalled bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		gotCtx, _ = authverify.FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	handler := gatewayAuthMiddlewareForTest(t)(next)

	tests := []struct {
		name        string
		method      string
		path        string
		staleCookie bool
		wantStatus  int
	}{
		{"share redeem is public despite stale cookie", http.MethodGet, "/share/0123456789abcdef0123456789abcdef", true, http.StatusOK},
		{"share redeem sub-path is public", http.MethodGet, "/share/tok/download", true, http.StatusOK},
		{"bare /share (no token) is NOT public", http.MethodGet, "/share", true, http.StatusUnauthorized},
		{"prefix requires the slash: /shares does not match", http.MethodGet, "/shares/x", true, http.StatusUnauthorized},
		{"share management is NOT public (legacy)", http.MethodPost, "/api/share/create", true, http.StatusUnauthorized},
		{"share management is NOT public (v1)", http.MethodPost, "/api/v1/share/create", true, http.StatusUnauthorized},
		{"share revoke DELETE is NOT public (v1)", http.MethodDelete, "/api/v1/share/some-share-id", true, http.StatusUnauthorized},
		{"share revoke DELETE is NOT public (legacy)", http.MethodDelete, "/api/share/some-share-id", true, http.StatusUnauthorized},
		{"login public (legacy)", http.MethodPost, "/auth/login", true, http.StatusOK},
		{"login public (v1)", http.MethodPost, "/api/v1/auth/login", true, http.StatusOK},
		{"signup public (legacy)", http.MethodPost, "/auth/signup", true, http.StatusOK},
		{"signup public (v1)", http.MethodPost, "/api/v1/auth/signup", true, http.StatusOK},
		{"refresh public (legacy)", http.MethodPost, "/auth/refresh", true, http.StatusOK},
		{"refresh public (v1)", http.MethodPost, "/api/v1/auth/refresh", true, http.StatusOK},
		{"plans public (legacy)", http.MethodGet, "/auth/plans", true, http.StatusOK},
		{"plans public (v1)", http.MethodGet, "/api/v1/auth/plans", true, http.StatusOK},
		{"flags public (legacy)", http.MethodGet, "/api/config/flags", true, http.StatusOK},
		{"flags public (v1)", http.MethodGet, "/api/v1/config/flags", true, http.StatusOK},
		{"forgot-password public despite stale cookie (legacy)", http.MethodPost, "/auth/forgot-password", true, http.StatusOK},
		{"forgot-password public despite stale cookie (v1)", http.MethodPost, "/api/v1/auth/forgot-password", true, http.StatusOK},
		{"forgot-password reachable without credentials", http.MethodPost, "/auth/forgot-password", false, http.StatusOK},
		{"reset-password public despite stale cookie (legacy)", http.MethodPost, "/auth/reset-password", true, http.StatusOK},
		{"reset-password public despite stale cookie (v1)", http.MethodPost, "/api/v1/auth/reset-password", true, http.StatusOK},
		{"reset-password reachable without credentials", http.MethodPost, "/auth/reset-password", false, http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextCalled = false
			gotCtx = authverify.AuthContext{}
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.staleCookie {
				req.AddCookie(&http.Cookie{Name: "access_token", Value: "expired-garbage-token"})
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("%s %s (staleCookie=%v): status = %d, want %d",
					tt.method, tt.path, tt.staleCookie, rec.Code, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusOK {
				if !nextCalled {
					t.Fatal("expected next handler to run")
				}
				// Every pass-through here is unverified, so the context must be
				// the guest plan — never a residue of the garbage cookie.
				if gotCtx.Plan != "guest" {
					t.Errorf("plan = %q, want guest", gotCtx.Plan)
				}
				if gotCtx.UserID != "" {
					t.Errorf("UserID = %q, want empty for unverified caller", gotCtx.UserID)
				}
			} else if nextCalled {
				t.Error("next handler ran on a rejected request")
			}
		})
	}
}

// TestPublicAuthPathsIncludePasswordReset is the regression guard for the
// stale-cookie lockout: forgot/reset-password must stay in publicAuthPaths()
// in both legacy and /v1 forms, or an expired access_token cookie blocks the
// reset flow at the gateway while auth-service itself serves it publicly.
func TestPublicAuthPathsIncludePasswordReset(t *testing.T) {
	want := []string{
		"/auth/forgot-password", "/auth/reset-password",
		apiVersionPrefix + "/auth/forgot-password", apiVersionPrefix + "/auth/reset-password",
	}
	got := make(map[string]bool)
	for _, p := range publicAuthPaths() {
		got[p] = true
	}
	for _, p := range want {
		if !got[p] {
			t.Errorf("publicAuthPaths() is missing %q — stale-cookie lockout on password reset", p)
		}
	}
}

// identityHeaders is every header ClearUserHeaders strips
// (shared/authverify/context.go:90-101). A forged value in ANY of them must
// never reach an upstream, because downstream services trust these headers as
// verified identity.
var identityHeaders = []string{
	"X-User-ID",
	"X-User-Role",
	"X-User-Scope",
	"X-User-Plan",
	"X-User-Plan-Max-File-MB",
	"X-User-Plan-Max-Files",
	"X-Impersonated-By",
}

// echoUpstream returns an httptest server that records the identity headers of
// the last request it received.
func echoUpstream(t *testing.T) (*httptest.Server, *http.Header) {
	t.Helper()
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

// forgeIdentityHeaders stamps attacker values onto every identity header.
func forgeIdentityHeaders(h http.Header) {
	for _, name := range identityHeaders {
		h.Set(name, "forged-"+name)
	}
}

// TestProxyStripsForgedIdentityHeaders pins the director's ordering contract
// (main.go newProxy: ClearUserHeaders BEFORE ApplyUserHeaders): forged inbound
// identity headers are erased, and when the request carries a verified
// AuthContext the gateway's values — not the client's — are what the upstream
// receives.
func TestProxyStripsForgedIdentityHeaders(t *testing.T) {
	backend, got := echoUpstream(t)
	handler := newProxy(routeConfig{
		prefix:         "/api/jobs",
		targetBasePath: "/api/jobs",
		targetURL:      backend.URL,
	}, circuitBreakerForTest())

	t.Run("anonymous request: all forged identity headers cleared", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/jobs/list", nil)
		forgeIdentityHeaders(req.Header)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		for _, name := range identityHeaders {
			if v := got.Get(name); v != "" {
				t.Errorf("upstream received forged %s = %q, want empty", name, v)
			}
		}
	})

	t.Run("verified context overwrites forged headers", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/jobs/list", nil)
		forgeIdentityHeaders(req.Header)
		req = authverify.SetRequestAuth(req, authverify.AuthContext{
			UserID: "real-user",
			Role:   "user",
			Plan:   "free",
		})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		want := map[string]string{
			"X-User-ID":   "real-user",
			"X-User-Role": "user",
			"X-User-Plan": "free",
		}
		for name, w := range want {
			if v := got.Get(name); v != w {
				t.Errorf("upstream %s = %q, want %q", name, v, w)
			}
		}
		// Fields the verified context does not carry must be ABSENT, not the
		// forged value: ApplyUserHeaders only sets non-empty fields, so a
		// leftover forged header would prove ClearUserHeaders was skipped.
		for _, name := range []string{"X-User-Scope", "X-Impersonated-By", "X-User-Plan-Max-File-MB", "X-User-Plan-Max-Files"} {
			if v := got.Get(name); v != "" {
				t.Errorf("upstream received forged %s = %q, want empty", name, v)
			}
		}
	})
}

// TestGatewayEndToEndForgedHeadersBecomeGuest runs the real middleware in front
// of the real route registration, the way main.go composes them: an anonymous
// caller forging X-User-ID must arrive at the upstream as a plain guest — no
// user ID, guest plan — proving the auth middleware and the proxy director
// cooperate (context wins over inbound headers) across the full wiring.
func TestGatewayEndToEndForgedHeadersBecomeGuest(t *testing.T) {
	backend, got := echoUpstream(t)

	mux := http.NewServeMux()
	registerServiceRoutes(mux, []routeConfig{
		{prefix: "/api/jobs", targetBasePath: "/api/jobs", targetURL: backend.URL},
	})
	handler := gatewayAuthMiddlewareForTest(t)(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	forgeIdentityHeaders(req.Header)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if v := got.Get("X-User-ID"); v != "" {
		t.Errorf("upstream X-User-ID = %q, want empty (forged identity must not survive)", v)
	}
	if v := got.Get("X-User-Role"); v != "" && v != "guest" {
		t.Errorf("upstream X-User-Role = %q, want empty or guest", v)
	}
	if v := got.Get("X-User-Plan"); v != "guest" {
		t.Errorf("upstream X-User-Plan = %q, want guest", v)
	}
	if v := got.Get("X-Impersonated-By"); v != "" {
		t.Errorf("upstream X-Impersonated-By = %q, want empty", v)
	}
}

// Tests for the route table and its trust boundaries, offline. What is
// provable without infrastructure: every documented route is registered on
// the right method and group, the protected group rejects unauthenticated and
// forged-token callers before any database access, and /internal fails closed
// (503) when INTERNAL_API_TOKEN is unset and rejects wrong tokens (401) when
// it is set. Handler outcomes past those gates (login, refresh, /readyz,
// GetAllPlans, valid-token /auth/me) need Postgres/Redis and are
// integration-tier.
package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"auth-service/internal/email"
	"auth-service/internal/token"
	"fyredocs/shared/authverify"
)

// newTestRouter builds the full production route table with the minimal env
// SetupRouter's collaborators need. The redis client is nil: the rate
// limiters fail open on a nil client by design, and no test here touches
// /readyz (the only other consumer). models.DB stays nil, so any request that
// reaches a database call would panic — passing tests therefore prove the
// guards fire first.
func newTestRouter(t *testing.T, internalToken string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	t.Setenv("JWT_HS256_SECRET", strings.Repeat("a1b2c3d4", 8))
	t.Setenv("JWT_ISSUER", "fyredocs-auth-test")
	t.Setenv("JWT_AUDIENCE", "fyredocs-test-clients")
	t.Setenv("INTERNAL_API_TOKEN", internalToken)

	issuer, err := token.NewIssuerFromEnv()
	if err != nil {
		t.Fatalf("NewIssuerFromEnv: %v", err)
	}
	verifier, err := authverify.NewVerifierFromEnv(nil)
	if err != nil {
		t.Fatalf("NewVerifierFromEnv: %v", err)
	}
	authMiddleware := authverify.GinAuthMiddleware(authverify.GinMiddlewareOptions{Verifier: verifier})

	r := gin.New()
	SetupRouter(r, issuer, nil, (*redis.Client)(nil), email.NoopMailer{}, authMiddleware)
	return r
}

type routesEnvelope struct {
	Success bool `json:"success"`
	Error   *struct {
		Code string `json:"code"`
	} `json:"error"`
}

func do(t *testing.T, r *gin.Engine, method, path string, headers map[string]string) (*httptest.ResponseRecorder, routesEnvelope) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var env routesEnvelope
	if rec.Body.Len() > 0 && strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode envelope (%d %s): %v", rec.Code, rec.Body.String(), err)
		}
	}
	return rec, env
}

// TestSetupRouter_RegistersDocumentedRoutes pins the route table itself: a
// route silently dropped from a group (or moved to the wrong method) is an
// API break the compiler cannot catch.
func TestSetupRouter_RegistersDocumentedRoutes(t *testing.T) {
	r := newTestRouter(t, "")

	registered := map[string]bool{}
	for _, route := range r.Routes() {
		registered[route.Method+" "+route.Path] = true
	}

	want := []string{
		// Public auth routes.
		"POST /auth/signup",
		"POST /auth/login",
		"POST /auth/refresh",
		"POST /auth/forgot-password",
		"POST /auth/reset-password",
		"GET /auth/plans",
		// Token-protected auth routes.
		"GET /auth/me",
		"GET /auth/profile",
		"POST /auth/logout",
		"PUT /auth/plan",
		"POST /auth/proxy-login",
		// Internal service-to-service API.
		"GET /internal/users/:id/plan",
		"POST /internal/users/:id/revoke-sessions",
		"DELETE /internal/sessions/:id",
		// Probes.
		"GET /healthz",
		"GET /readyz",
	}
	for _, route := range want {
		if !registered[route] {
			t.Errorf("route %q is not registered", route)
		}
	}
}

func TestProtectedRoutesRejectUnauthenticated(t *testing.T) {
	r := newTestRouter(t, "")

	// No credentials: the auth middleware passes the request through as
	// anonymous, and the handler's own identity gate must answer 401 before
	// any database access (models.DB is nil — a miss would panic, not 401).
	for _, path := range []string{"/auth/me", "/auth/profile"} {
		t.Run("anonymous "+path, func(t *testing.T) {
			rec, env := do(t, r, http.MethodGet, path, nil)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
			}
			if env.Error == nil || env.Error.Code != "AUTH_UNAUTHORIZED" {
				t.Errorf("error = %+v, want code AUTH_UNAUTHORIZED", env.Error)
			}
		})
	}

	t.Run("forged bearer token rejected by middleware", func(t *testing.T) {
		rec, _ := do(t, r, http.MethodGet, "/auth/me", map[string]string{
			"Authorization": "Bearer not.a.jwt",
		})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 for an invalid token", rec.Code)
		}
	})

	t.Run("forged access_token cookie rejected", func(t *testing.T) {
		rec, _ := do(t, r, http.MethodGet, "/auth/me", map[string]string{
			"Cookie": "access_token=forged-token-value",
		})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 for a forged cookie", rec.Code)
		}
	})
}

// TestInternalFailsClosedWhenTokenUnset pins the fail-closed posture of
// InternalTokenGin: with INTERNAL_API_TOKEN unset, every /internal request is
// refused with 503 — a missing variable must never reopen the mesh.
func TestInternalFailsClosedWhenTokenUnset(t *testing.T) {
	r := newTestRouter(t, "")

	requests := []struct{ method, path string }{
		{http.MethodGet, "/internal/users/not-a-uuid/plan"},
		{http.MethodPost, "/internal/users/not-a-uuid/revoke-sessions"},
		{http.MethodDelete, "/internal/sessions/not-a-uuid"},
	}
	for _, req := range requests {
		t.Run(req.method+" "+req.path, func(t *testing.T) {
			rec, env := do(t, r, req.method, req.path, map[string]string{
				// Even a caller presenting some token is refused: there is no
				// configured secret to match against.
				authverify.InternalTokenHeader: "anything",
			})
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503 fail-closed (body %s)", rec.Code, rec.Body.String())
			}
			if env.Error == nil || env.Error.Code != "SERVICE_UNAVAILABLE" {
				t.Errorf("error = %+v, want code SERVICE_UNAVAILABLE", env.Error)
			}
		})
	}
}

func TestInternalTokenEnforcement(t *testing.T) {
	const secret = "internal-test-secret"
	r := newTestRouter(t, secret)
	path := "/internal/users/" + uuid.NewString() + "/plan"

	t.Run("missing token rejected", func(t *testing.T) {
		rec, env := do(t, r, http.MethodGet, path, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
		}
		if env.Error == nil || env.Error.Code != "AUTH_UNAUTHORIZED" {
			t.Errorf("error = %+v, want code AUTH_UNAUTHORIZED", env.Error)
		}
	})

	t.Run("wrong token rejected", func(t *testing.T) {
		rec, _ := do(t, r, http.MethodGet, path, map[string]string{
			authverify.InternalTokenHeader: "wrong-secret",
		})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 for a wrong token", rec.Code)
		}
	})

	t.Run("correct token reaches the handler", func(t *testing.T) {
		// An invalid user id makes the handler answer 400 before its DB
		// lookup — proving the middleware passed the request through while
		// keeping the test offline (models.DB is nil).
		rec, env := do(t, r, http.MethodGet, "/internal/users/not-a-uuid/plan", map[string]string{
			authverify.InternalTokenHeader: secret,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 from the handler's own guard (body %s)", rec.Code, rec.Body.String())
		}
		if env.Error == nil || env.Error.Code != "INVALID_INPUT" {
			t.Errorf("error = %+v, want code INVALID_INPUT", env.Error)
		}
	})
}

func TestHealthz(t *testing.T) {
	r := newTestRouter(t, "")
	rec, _ := do(t, r, http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("body = %q, want ok", rec.Body.String())
	}
}

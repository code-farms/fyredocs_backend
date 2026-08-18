package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"fyredocs/shared/authverify"
)

// buildRouter constructs the real production router with INTERNAL_API_TOKEN
// pinned via t.Setenv, because SetupRouter reads the variable once at
// registration time — setting it after registration would change nothing.
func buildRouter(t *testing.T, internalToken string) *gin.Engine {
	t.Helper()
	t.Setenv("INTERNAL_API_TOKEN", internalToken)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetupRouter(r)
	return r
}

// TestDocumentedRoutesRegistered is the drift guard between this file and the
// service's documented API surface: a route that silently disappears from
// SetupRouter fails here instead of at a consumer.
func TestDocumentedRoutesRegistered(t *testing.T) {
	r := buildRouter(t, "test-secret")
	got := make(map[string]bool)
	for _, ri := range r.Routes() {
		got[ri.Method+" "+ri.Path] = true
	}
	want := []string{
		"GET /healthz",
		"GET /readyz",
		"POST /internal/notifications",
		"GET /api/notifications",
		"GET /api/notifications/stream",
		"POST /api/notifications/read-all",
		"POST /api/notifications/:id/read",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("documented route %q is not registered", w)
		}
	}
}

// TestFeedRoutesRequireAuth sweeps every /api/notifications route — the SSE
// stream included — unauthenticated. SetupRouter mounts RequireUser but NOT the
// JWT middleware (main.go owns that), so no request through this bare router
// carries an AuthContext: every route must 401 before touching the (nil)
// database or the NATS connection. Each request also carries a forged
// X-User-ID header to re-pin that the raw header alone grants no feed access.
func TestFeedRoutesRequireAuth(t *testing.T) {
	r := buildRouter(t, "test-secret")
	id := uuid.Must(uuid.NewV7()).String()
	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/notifications"},
		{http.MethodGet, "/api/notifications/stream"},
		{http.MethodPost, "/api/notifications/read-all"},
		{http.MethodPost, "/api/notifications/" + id + "/read"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("X-User-ID", uuid.Must(uuid.NewV7()).String())
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s unauthenticated: expected 401, got %d", tc.method, tc.path, w.Code)
		}
	}
}

// TestInternalCreateFailsClosedWhenUnconfigured pins the posture documented on
// authverify.InternalTokenGin: with INTERNAL_API_TOKEN unset, the mesh-only
// create refuses everything with 503 — network isolation must never be the
// only defense on this route.
func TestInternalCreateFailsClosedWhenUnconfigured(t *testing.T) {
	r := buildRouter(t, "")
	req := httptest.NewRequest(http.MethodPost, "/internal/notifications", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(authverify.InternalTokenHeader, "anything")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("unconfigured internal token: expected 503, got %d", w.Code)
	}
}

// TestInternalCreateTokenChain walks the env-wired guard chain in order: a
// mismatched secret dies at 401 before the body is read, and a matching secret
// with an invalid body dies at 400 in validation. models.DB is nil, so the 400
// row also proves the rejected body never reached persistence.
func TestInternalCreateTokenChain(t *testing.T) {
	const secret = "test-internal-secret"
	r := buildRouter(t, secret)
	cases := []struct {
		name, token, body string
		want              int
	}{
		{"wrong secret", "wrong", `{"userId":"x","title":"hi"}`, http.StatusUnauthorized},
		{"valid secret, invalid body", secret, `{"userId":"not-a-uuid","title":"hi"}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/internal/notifications", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(authverify.InternalTokenHeader, tc.token)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Errorf("expected %d, got %d (body %q)", tc.want, w.Code, w.Body.String())
			}
		})
	}
}

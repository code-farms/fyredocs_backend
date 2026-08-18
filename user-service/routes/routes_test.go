package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// errorCode extracts error.code from the standard response envelope (CLAUDE.md §7)
// so tests can distinguish which guard fired when two guards share a status.
func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response envelope: %v (body %q)", err, w.Body.String())
	}
	return body.Error.Code
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
		"GET /api/orgs",
		"POST /api/orgs",
		"GET /api/orgs/:id",
		"GET /api/orgs/:id/members",
		"POST /api/orgs/:id/members",
		"PATCH /api/orgs/:id/members/:userId",
		"DELETE /api/orgs/:id/members/:userId",
		"GET /internal/v1/orgs/:id",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("documented route %q is not registered", w)
		}
	}
}

// TestPublicOrgRoutesRequireAuth sweeps every /api/orgs route unauthenticated.
// SetupRouter mounts RequireUser but NOT the JWT middleware (main.go owns
// that), so no request through this bare router carries an AuthContext — every
// route must therefore 401 before touching the (nil) database. Each request
// also carries a forged X-User-ID header to re-pin that the raw header alone
// grants nothing on the public tier.
func TestPublicOrgRoutesRequireAuth(t *testing.T) {
	r := buildRouter(t, "test-secret")
	org := uuid.Must(uuid.NewV7()).String()
	member := uuid.Must(uuid.NewV7()).String()
	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/orgs"},
		{http.MethodPost, "/api/orgs"},
		{http.MethodGet, "/api/orgs/" + org},
		{http.MethodGet, "/api/orgs/" + org + "/members"},
		{http.MethodPost, "/api/orgs/" + org + "/members"},
		{http.MethodPatch, "/api/orgs/" + org + "/members/" + member},
		{http.MethodDelete, "/api/orgs/" + org + "/members/" + member},
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

// TestInternalOrgLookupFailsClosedWhenUnconfigured pins the posture documented
// on authverify.InternalTokenGin: with INTERNAL_API_TOKEN unset the internal
// tier refuses everything with 503 — even a caller presenting a token — rather
// than reopening the mesh. The org id is deliberately non-parsable so a
// fail-open regression surfaces as a wrong status, not a nil-DB panic.
func TestInternalOrgLookupFailsClosedWhenUnconfigured(t *testing.T) {
	r := buildRouter(t, "")
	for _, withToken := range []bool{false, true} {
		req := httptest.NewRequest(http.MethodGet, "/internal/v1/orgs/not-a-uuid", nil)
		if withToken {
			req.Header.Set(authverify.InternalTokenHeader, "anything")
		}
		req.Header.Set("X-User-ID", uuid.Must(uuid.NewV7()).String())
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("unconfigured internal token (header sent=%v): expected 503, got %d", withToken, w.Code)
		}
	}
}

// TestInternalOrgLookupTokenChain walks the guarded chain of the mesh route in
// order — shared secret, then identity header, then the org-id parse — proving
// each middleware rejects before the next runs and that the whole pre-DB chain
// is reachable without a database.
func TestInternalOrgLookupTokenChain(t *testing.T) {
	const secret = "test-internal-secret"
	r := buildRouter(t, secret)
	uid := uuid.Must(uuid.NewV7()).String()

	cases := []struct {
		name        string
		token, user string
		wantStatus  int
		wantCode    string
	}{
		{"wrong secret rejected before identity", "wrong", uid, http.StatusUnauthorized, "AUTH_UNAUTHORIZED"},
		{"missing identity header rejected after secret", secret, "", http.StatusBadRequest, "INVALID_INPUT"},
		{"bad org id rejected in handler", secret, uid, http.StatusBadRequest, "INVALID_ID"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/internal/v1/orgs/not-a-uuid", nil)
			req.Header.Set(authverify.InternalTokenHeader, tc.token)
			if tc.user != "" {
				req.Header.Set("X-User-ID", tc.user)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.wantStatus {
				t.Fatalf("expected %d, got %d (body %q)", tc.wantStatus, w.Code, w.Body.String())
			}
			if got := errorCode(t, w); got != tc.wantCode {
				t.Errorf("error code = %q, want %q", got, tc.wantCode)
			}
		})
	}
}

// Test harness for handler guard-path tests. Everything here runs fully
// offline: models.DB stays nil, so every request MUST stop at a guard
// (auth, input validation, org resolution) before any query would run — a
// test that lets a handler reach the database panics loudly on the nil DB,
// which is the desired failure mode for a guard that stopped guarding.
package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"fyredocs/shared/authverify"
	"fyredocs/shared/response"
)

// doAuthed mounts a single handler behind RequireUser with a verified identity
// (the way main.go's auth middleware would seed it after checking the JWT) and
// performs one request. body == "" sends no body at all, modelling a client
// that omitted the JSON payload entirely.
func doAuthed(t *testing.T, uid uuid.UUID, method, pattern string, h gin.HandlerFunc, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		authverify.SetGinAuth(c, authverify.AuthContext{UserID: uid.String()})
		c.Next()
	})
	r.Use(RequireUser())
	r.Handle(method, pattern, h)

	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// envelope decodes the standard response envelope (CLAUDE.md §7). Failing to
// decode is a test failure in itself: every handler error path must speak the
// envelope, not a bare status.
func envelope(t *testing.T, w *httptest.ResponseRecorder) response.APIResponse {
	t.Helper()
	var env response.APIResponse
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("response is not the standard envelope: %v (body %q)", err, w.Body.String())
	}
	return env
}

// wantErrEnvelope asserts status + machine-readable error code, the two fields
// clients switch on (shared/response/codes.go calls them a stable contract).
func wantErrEnvelope(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status = %d, want %d (body %q)", w.Code, status, w.Body.String())
	}
	env := envelope(t, w)
	if env.Success {
		t.Errorf("success = true on an error response")
	}
	if env.Error == nil {
		t.Fatalf("error envelope missing (body %q)", w.Body.String())
	}
	if env.Error.Code != code {
		t.Errorf("error.code = %q, want %q", env.Error.Code, code)
	}
}

// withUserService points orgclient at a stub user-service for the duration of
// the test (USER_SERVICE_URL is read per call, so t.Setenv is the whole seam).
func withUserService(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	t.Setenv("USER_SERVICE_URL", srv.URL)
	return srv
}

// membershipRole answers every membership lookup with a 200 and the given org
// role, the shape user-service's /internal/v1/orgs/:id returns for a member.
func membershipRole(role string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"role": role}})
	}
}

// membershipStatus answers every membership lookup with a bare status code
// (404 = not a member, 500 = user-service failure).
func membershipStatus(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }
}

// noUserService pins that a code path performs NO membership lookup: any call
// to the stub fails the test. Guards that reject before authorization (invalid
// orgId, personal scope) must not spend a network round-trip first.
func noUserService(t *testing.T) {
	t.Helper()
	withUserService(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected user-service call: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	})
}

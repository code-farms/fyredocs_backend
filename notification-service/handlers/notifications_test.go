package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"fyredocs/shared/authverify"
	"fyredocs/shared/natsconn"
)

// serveWithAuth runs a RequireUser-guarded route, optionally seeding the
// AuthContext the way the auth middleware would after verifying a JWT. Passing a
// nil authCtx models an unauthenticated request that reached the handler chain.
func serveWithAuth(authCtx *authverify.AuthContext, mutate func(*http.Request)) int {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if authCtx != nil {
		r.Use(func(c *gin.Context) {
			authverify.SetGinAuth(c, *authCtx)
			c.Next()
		})
	}
	r.Use(RequireUser())
	r.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if mutate != nil {
		mutate(req)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

// TestRequireUserRejectsForgedHeader is the regression guard for the multi-host
// split: identity must come from the verified AuthContext, never from the raw
// X-User-ID header. Before local JWT verification this header alone granted
// access, so a service reachable from anything but the gateway could have another
// user's notification feed read out of it.
func TestRequireUserRejectsForgedHeader(t *testing.T) {
	forged := uuid.Must(uuid.NewV7()).String()
	code := serveWithAuth(nil, func(req *http.Request) {
		req.Header.Set("X-User-ID", forged)
	})
	if code != http.StatusUnauthorized {
		t.Errorf("forged X-User-ID with no verified identity: expected 401, got %d", code)
	}
}

func TestRequireUser(t *testing.T) {
	if code := serveWithAuth(nil, nil); code != http.StatusUnauthorized {
		t.Errorf("no identity: expected 401, got %d", code)
	}

	valid := authverify.AuthContext{UserID: uuid.Must(uuid.NewV7()).String()}
	if code := serveWithAuth(&valid, nil); code != http.StatusOK {
		t.Errorf("verified identity: expected 200, got %d", code)
	}

	if code := serveWithAuth(&authverify.AuthContext{UserID: "garbage"}, nil); code != http.StatusUnauthorized {
		t.Errorf("malformed subject: expected 401, got %d", code)
	}

	guest := authverify.AuthContext{Role: "guest", IsGuest: true}
	if code := serveWithAuth(&guest, nil); code != http.StatusUnauthorized {
		t.Errorf("guest identity: expected 401, got %d", code)
	}
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

// feedRouter mounts the real feed handlers behind RequireUser exactly as
// routes.SetupRouter composes them, optionally seeding a verified identity.
// models.DB is nil in unit tests, so every request sent through this router
// must be stopped by a pre-DB guard — reaching a query panics and fails the
// test loudly rather than passing vacuously.
func feedRouter(authCtx *authverify.AuthContext) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if authCtx != nil {
		r.Use(func(c *gin.Context) {
			authverify.SetGinAuth(c, *authCtx)
			c.Next()
		})
	}
	n := r.Group("/api/notifications")
	n.Use(RequireUser())
	{
		n.GET("/stream", StreamNotifications)
		n.POST("/:id/read", MarkRead)
	}
	return r
}

// TestMarkReadRejectsInvalidID pins that the :id parse guards the read-state
// mutation before the UPDATE is built — a garbage id must die at 400, never
// reach the database.
func TestMarkReadRejectsInvalidID(t *testing.T) {
	auth := authverify.AuthContext{UserID: uuid.Must(uuid.NewV7()).String()}
	r := feedRouter(&auth)
	req := httptest.NewRequest(http.MethodPost, "/api/notifications/not-a-uuid/read", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body %q)", w.Code, w.Body.String())
	}
	if got := errorCode(t, w); got != "INVALID_ID" {
		t.Errorf("error code = %q, want INVALID_ID", got)
	}
}

// TestStreamUnavailableWithoutNATS pins that SSE degrades to a clean 503 when
// the fan-out bus is down, instead of subscribing against a nil connection.
// The durable feed stays served by ListNotifications, so 503 (not 500) is the
// contract clients rely on to fall back to polling.
func TestStreamUnavailableWithoutNATS(t *testing.T) {
	old := natsconn.Conn
	natsconn.Conn = nil
	t.Cleanup(func() { natsconn.Conn = old })

	auth := authverify.AuthContext{UserID: uuid.Must(uuid.NewV7()).String()}
	r := feedRouter(&auth)
	req := httptest.NewRequest(http.MethodGet, "/api/notifications/stream", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d (body %q)", w.Code, w.Body.String())
	}
}

// internalRouter mounts CreateInternal behind InternalTokenGin with a directly
// supplied secret, mirroring routes.SetupRouter's composition (the env-wired
// variant is exercised in routes/routes_test.go).
func internalRouter(secret string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/internal/notifications", authverify.InternalTokenGin(secret), CreateInternal)
	return r
}

// postInternal serves one internal create with the given token and JSON body.
func postInternal(r *gin.Engine, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/internal/notifications", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set(authverify.InternalTokenHeader, token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestCreateInternalFailsClosedWithoutToken pins the posture documented on
// authverify.InternalTokenGin: an unconfigured secret refuses every internal
// create with 503, even a well-formed one, rather than leaving the mesh
// endpoint unauthenticated.
func TestCreateInternalFailsClosedWithoutToken(t *testing.T) {
	r := internalRouter("")
	body := `{"userId":"` + uuid.Must(uuid.NewV7()).String() + `","type":"info","title":"hi"}`
	if w := postInternal(r, "anything", body); w.Code != http.StatusServiceUnavailable {
		t.Errorf("unconfigured secret: expected 503, got %d", w.Code)
	}
	if w := postInternal(r, "", body); w.Code != http.StatusServiceUnavailable {
		t.Errorf("unconfigured secret, no header: expected 503, got %d", w.Code)
	}
}

// TestCreateInternalRejectsWrongToken pins that a configured secret rejects a
// mismatched token with 401 before the body is even bound.
func TestCreateInternalRejectsWrongToken(t *testing.T) {
	r := internalRouter("test-internal-secret")
	if w := postInternal(r, "wrong", `{"userId":"x"}`); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong secret: expected 401, got %d", w.Code)
	}
}

// TestCreateInternalRejectsInvalidBody sweeps the pre-persistence validation of
// the internal create body with a valid token. models.DB is nil, so a 400 here
// simultaneously proves the guard fired AND that no row (or dedupe query) was
// attempted — any DB touch would panic the test.
func TestCreateInternalRejectsInvalidBody(t *testing.T) {
	uid := uuid.Must(uuid.NewV7()).String()
	long := func(n int) string { return strings.Repeat("a", n) }
	cases := []struct {
		name     string
		body     string
		wantCode string
	}{
		{"malformed json", `{"userId":`, ""},
		{"empty body", `{}`, ""},
		{"missing userId", `{"type":"info","title":"hi"}`, ""},
		{"non-uuid userId", `{"userId":"not-a-uuid","type":"info","title":"hi"}`, ""},
		{"missing title", `{"userId":"` + uid + `","type":"info"}`, ""},
		{"whitespace-only title survives binding, dies on trim", `{"userId":"` + uid + `","type":"info","title":"   "}`, "INVALID_TITLE"},
		{"title over 200 chars", `{"userId":"` + uid + `","type":"info","title":"` + long(201) + `"}`, ""},
		{"type over 64 chars", `{"userId":"` + uid + `","type":"` + long(65) + `","title":"hi"}`, ""},
		{"link over 512 chars", `{"userId":"` + uid + `","type":"info","title":"hi","link":"` + long(513) + `"}`, ""},
		{"body over 2000 chars", `{"userId":"` + uid + `","type":"info","title":"hi","body":"` + long(2001) + `"}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := internalRouter("test-internal-secret")
			w := postInternal(r, "test-internal-secret", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d (body %q)", w.Code, w.Body.String())
			}
			if tc.wantCode != "" {
				if got := errorCode(t, w); got != tc.wantCode {
					t.Errorf("error code = %q, want %q", got, tc.wantCode)
				}
			}
		})
	}
}

func TestNotificationRoutePatterns(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	n := r.Group("/api/notifications")
	ok := func(c *gin.Context) { c.Status(http.StatusOK) }
	n.GET("", ok)
	n.POST("/read-all", ok)
	n.POST("/:id/read", ok)

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/notifications"},
		{http.MethodPost, "/api/notifications/read-all"},
		{http.MethodPost, "/api/notifications/abc/read"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("%s %s: expected 200, got %d", tc.method, tc.path, w.Code)
		}
	}
}

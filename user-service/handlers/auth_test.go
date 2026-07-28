package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"fyredocs/shared/authverify"

	"user-service/internal/models"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Acme Inc.":      "acme-inc",
		"  Hello World ": "hello-world",
		"@@@":            "org",
		"":               "org",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q)=%q want %q", in, got, want)
		}
	}
}

func TestRoleAtLeast(t *testing.T) {
	if !models.RoleAtLeast(models.RoleOwner, models.RoleAdmin) {
		t.Error("owner should satisfy admin")
	}
	if !models.RoleAtLeast(models.RoleAdmin, models.RoleAdmin) {
		t.Error("admin should satisfy admin")
	}
	if models.RoleAtLeast(models.RoleEditor, models.RoleAdmin) {
		t.Error("editor should NOT satisfy admin")
	}
	if models.RoleAtLeast("bogus", models.RoleViewer) {
		t.Error("unknown role should satisfy nothing")
	}
	if !models.ValidRole(models.RoleViewer) || models.ValidRole("nope") {
		t.Error("ValidRole mismatch")
	}
}

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
// access, so a service reachable from anything but the gateway could be
// impersonated — and here that means acting inside another tenant's organization.
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
}

// TestInternalIdentity covers the service-to-service path. document-service holds
// the end user's ID but no JWT, so this middleware converts the header into an
// identity — but only ever behind InternalTokenGin, which this test asserts is
// what the route group composes (see routes.SetupRouter).
func TestInternalIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const secret = "test-internal-secret"

	r := gin.New()
	internal := r.Group("/internal/v1",
		authverify.InternalTokenGin(secret),
		InternalIdentity(),
	)
	internal.GET("/whoami", func(c *gin.Context) {
		id, ok := userID(c)
		if !ok {
			c.String(http.StatusUnauthorized, "no identity")
			return
		}
		c.String(http.StatusOK, id.String())
	})

	uid := uuid.Must(uuid.NewV7()).String()

	// Correct secret + user header → identity resolves.
	req := httptest.NewRequest(http.MethodGet, "/internal/v1/whoami", nil)
	req.Header.Set(authverify.InternalTokenHeader, secret)
	req.Header.Set("X-User-ID", uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != uid {
		t.Errorf("valid internal call: expected 200 %s, got %d %s", uid, w.Code, w.Body.String())
	}

	// Right user header, WRONG secret → rejected. This is the property that keeps
	// the header from being a bypass of the JWT check on the public routes.
	req = httptest.NewRequest(http.MethodGet, "/internal/v1/whoami", nil)
	req.Header.Set(authverify.InternalTokenHeader, "wrong")
	req.Header.Set("X-User-ID", uid)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("wrong internal token: expected 401, got %d", w.Code)
	}

	// No secret at all → rejected.
	req = httptest.NewRequest(http.MethodGet, "/internal/v1/whoami", nil)
	req.Header.Set("X-User-ID", uid)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("missing internal token: expected 401, got %d", w.Code)
	}

	// Valid secret but no user header → 400, not a silent anonymous success.
	req = httptest.NewRequest(http.MethodGet, "/internal/v1/whoami", nil)
	req.Header.Set(authverify.InternalTokenHeader, secret)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing X-User-ID: expected 400, got %d", w.Code)
	}
}

// TestInternalTokenFailsClosed pins the posture documented on InternalTokenGin:
// an unset INTERNAL_API_TOKEN must refuse every internal request rather than
// leaving the mesh unauthenticated.
func TestInternalTokenFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	internal := r.Group("/internal/v1", authverify.InternalTokenGin(""), InternalIdentity())
	internal.GET("/whoami", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodGet, "/internal/v1/whoami", nil)
	req.Header.Set("X-User-ID", uuid.Must(uuid.NewV7()).String())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("unset internal token: expected 503, got %d", w.Code)
	}
}

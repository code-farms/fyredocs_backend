package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"fyredocs/shared/authverify"
)

// seedAuth stands in for the auth middleware installed in main.go, populating the
// AuthContext the way a verified JWT would. Tests use it instead of setting
// X-User-* headers because adminAuth deliberately no longer reads those headers —
// see TestAdminAuth_ForgedHeaders_Rejected.
func seedAuth(userID, role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		authverify.SetGinAuth(c, authverify.AuthContext{UserID: userID, Role: role})
		c.Next()
	}
}

// newTestRouter builds an adminAuth-guarded route. A nil seed models a request
// that arrived with no verifiable credentials.
func newTestRouter(seed gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if seed != nil {
		r.Use(seed)
	}
	r.Use(adminAuth())
	r.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	return r
}

func serve(r *gin.Engine, path string, mutate func(*http.Request)) int {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if mutate != nil {
		mutate(req)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

// TestAdminAuth_ForgedHeaders_Rejected is the regression guard for the multi-host
// split. adminAuth once read X-User-Role straight off the request, so anyone who
// could reach this service's port could claim super-admin and read platform-wide
// metrics. Identity now comes only from the verified AuthContext; if this test
// starts seeing 200, that hole is back open.
func TestAdminAuth_ForgedHeaders_Rejected(t *testing.T) {
	r := newTestRouter(nil)

	code := serve(r, "/test", func(req *http.Request) {
		req.Header.Set("X-User-ID", "attacker-uuid")
		req.Header.Set("X-User-Role", "super-admin")
	})
	if code != http.StatusUnauthorized {
		t.Errorf("forged super-admin headers with no verified identity: expected 401, got %d", code)
	}
}

func TestAdminAuth_NoIdentity_Returns401(t *testing.T) {
	if code := serve(newTestRouter(nil), "/test", nil); code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", code)
	}
}

func TestAdminAuth_RegularUser_Returns403(t *testing.T) {
	r := newTestRouter(seedAuth("some-uuid", "user"))
	if code := serve(r, "/test", nil); code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", code)
	}
}

func TestAdminAuth_SuperAdmin_Passes(t *testing.T) {
	r := newTestRouter(seedAuth("admin-uuid", "super-admin"))
	if code := serve(r, "/test", nil); code != http.StatusOK {
		t.Errorf("expected 200, got %d", code)
	}
}

// TestAdminAuth_EmptyRole_Returns401 covers a verified token whose claims carry no
// role and no subject — authenticated in form but naming nobody, so it must read
// as "not logged in" rather than falling through to the 403 branch.
func TestAdminAuth_EmptyRole_Returns401(t *testing.T) {
	r := newTestRouter(seedAuth("", ""))
	if code := serve(r, "/test", nil); code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", code)
	}
}

func TestAuthzProbe_GatedByAdminAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newRouter := func(seed gin.HandlerFunc) *gin.Engine {
		r := gin.New()
		if seed != nil {
			r.Use(seed)
		}
		admin := r.Group("/admin")
		admin.Use(adminAuth())
		admin.GET("/authz", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
		return r
	}

	cases := []struct {
		name   string
		userID string
		role   string
		seeded bool
		want   int
	}{
		{"no identity -> 401", "", "", false, http.StatusUnauthorized},
		{"regular user -> 403", "some-uuid", "user", true, http.StatusForbidden},
		{"super-admin -> 200", "admin-uuid", "super-admin", true, http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var seed gin.HandlerFunc
			if tc.seeded {
				seed = seedAuth(tc.userID, tc.role)
			}
			if code := serve(newRouter(seed), "/admin/authz", nil); code != tc.want {
				t.Errorf("expected %d, got %d", tc.want, code)
			}
		})
	}
}

func TestNewEndpoints_RequireAdminAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)

	endpoints := []string{
		"/admin/metrics/business",
		"/admin/metrics/growth",
		"/admin/metrics/engagement",
		"/admin/metrics/reliability",
		"/admin/metrics/system",
		"/admin/metrics/server-performance",
		"/admin/metrics/api-performance",
	}

	newRouter := func(seed gin.HandlerFunc) *gin.Engine {
		r := gin.New()
		if seed != nil {
			r.Use(seed)
		}
		admin := r.Group("/admin")
		admin.Use(adminAuth())
		for _, p := range []string{
			"/metrics/business", "/metrics/growth", "/metrics/engagement",
			"/metrics/reliability", "/metrics/system",
			"/metrics/server-performance", "/metrics/api-performance",
		} {
			admin.GET(p, func(c *gin.Context) { c.String(http.StatusOK, "ok") })
		}
		return r
	}

	for _, ep := range endpoints {
		t.Run(ep+" requires auth", func(t *testing.T) {
			// No identity -> 401
			if code := serve(newRouter(nil), ep, nil); code != http.StatusUnauthorized {
				t.Errorf("%s: expected 401 without auth, got %d", ep, code)
			}

			// Forged admin headers with no verified identity -> still 401.
			code := serve(newRouter(nil), ep, func(req *http.Request) {
				req.Header.Set("X-User-ID", "attacker-uuid")
				req.Header.Set("X-User-Role", "super-admin")
			})
			if code != http.StatusUnauthorized {
				t.Errorf("%s: expected 401 for forged headers, got %d", ep, code)
			}

			// Regular user -> 403
			if code := serve(newRouter(seedAuth("some-uuid", "user")), ep, nil); code != http.StatusForbidden {
				t.Errorf("%s: expected 403 for regular user, got %d", ep, code)
			}

			// Super admin -> 200
			if code := serve(newRouter(seedAuth("admin-uuid", "super-admin")), ep, nil); code != http.StatusOK {
				t.Errorf("%s: expected 200 for super-admin, got %d", ep, code)
			}
		})
	}
}

// TestActivityRoutes_Registered pins the activity tier decisions: ingest must
// accept anonymous callers (it 400s on an empty body, proving the request
// reached the handler rather than an auth gate), and the personal feed must
// turn anonymous callers away with 401 inside the handler.
func TestActivityRoutes_Registered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetupRouter(r)

	req := httptest.NewRequest(http.MethodPost, "/api/activity/events", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("anonymous POST /api/activity/events with empty body: code = %d, want 400", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/activity/me", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous GET /api/activity/me: code = %d, want 401", w.Code)
	}
}

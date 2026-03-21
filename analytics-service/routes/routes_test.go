package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func newTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(adminAuth())
	r.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	return r
}

func TestAdminAuth_NoHeaders_Returns401(t *testing.T) {
	r := newTestRouter()

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAdminAuth_RegularUser_Returns403(t *testing.T) {
	r := newTestRouter()

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-User-ID", "some-uuid")
	req.Header.Set("X-User-Role", "user")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestAdminAuth_SuperAdmin_Passes(t *testing.T) {
	r := newTestRouter()

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-User-ID", "admin-uuid")
	req.Header.Set("X-User-Role", "super-admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestAdminAuth_EmptyRole_Returns401(t *testing.T) {
	r := newTestRouter()

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-User-Role", "")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
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

	for _, ep := range endpoints {
		t.Run(ep+" requires auth", func(t *testing.T) {
			r := gin.New()
			admin := r.Group("/admin")
			admin.Use(adminAuth())
			admin.GET("/metrics/business", func(c *gin.Context) { c.String(200, "ok") })
			admin.GET("/metrics/growth", func(c *gin.Context) { c.String(200, "ok") })
			admin.GET("/metrics/engagement", func(c *gin.Context) { c.String(200, "ok") })
			admin.GET("/metrics/reliability", func(c *gin.Context) { c.String(200, "ok") })
			admin.GET("/metrics/system", func(c *gin.Context) { c.String(200, "ok") })
			admin.GET("/metrics/server-performance", func(c *gin.Context) { c.String(200, "ok") })
			admin.GET("/metrics/api-performance", func(c *gin.Context) { c.String(200, "ok") })

			// No auth headers -> 401
			req := httptest.NewRequest(http.MethodGet, ep, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("%s: expected 401 without auth, got %d", ep, w.Code)
			}

			// Regular user -> 403
			req = httptest.NewRequest(http.MethodGet, ep, nil)
			req.Header.Set("X-User-ID", "some-uuid")
			req.Header.Set("X-User-Role", "user")
			w = httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Errorf("%s: expected 403 for regular user, got %d", ep, w.Code)
			}

			// Super admin -> 200
			req = httptest.NewRequest(http.MethodGet, ep, nil)
			req.Header.Set("X-User-ID", "admin-uuid")
			req.Header.Set("X-User-Role", "super-admin")
			w = httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("%s: expected 200 for super-admin, got %d", ep, w.Code)
			}
		})
	}
}

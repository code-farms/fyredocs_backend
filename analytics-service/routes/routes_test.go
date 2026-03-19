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

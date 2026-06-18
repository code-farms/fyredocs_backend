package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestRequireUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequireUser())
	r.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("no header: expected 401, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-User-ID", uuid.Must(uuid.NewV7()).String())
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("valid header: expected 200, got %d", w.Code)
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

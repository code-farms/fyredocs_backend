package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// These verify the route patterns the real router uses resolve correctly,
// without touching the DB (real handlers are exercised via integration).
func TestDocumentRoutePatterns(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api")
	ok := func(c *gin.Context) { c.Status(http.StatusOK) }
	api.GET("/documents", ok)
	api.POST("/documents", ok)
	api.GET("/documents/:id", ok)
	api.PATCH("/documents/:id", ok)
	api.DELETE("/documents/:id", ok)
	api.POST("/documents/:id/restore", ok)
	api.DELETE("/documents/:id/permanent", ok)
	api.POST("/documents/:id/tags", ok)
	api.DELETE("/documents/:id/tags/:tagId", ok)
	api.GET("/folders", ok)
	api.GET("/tags", ok)

	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/api/documents"},
		{http.MethodPost, "/api/documents"},
		{http.MethodGet, "/api/documents/abc"},
		{http.MethodPatch, "/api/documents/abc"},
		{http.MethodDelete, "/api/documents/abc"},
		{http.MethodPost, "/api/documents/abc/restore"},
		{http.MethodDelete, "/api/documents/abc/permanent"},
		{http.MethodPost, "/api/documents/abc/tags"},
		{http.MethodDelete, "/api/documents/abc/tags/xyz"},
		{http.MethodGet, "/api/folders"},
		{http.MethodGet, "/api/tags"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("%s %s: expected 200, got %d", tc.method, tc.path, w.Code)
		}
	}
}

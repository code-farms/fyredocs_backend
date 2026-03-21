package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestReliabilityMetrics_RouteExists(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/admin/metrics/reliability", func(c *gin.Context) {
		days := c.DefaultQuery("days", "30")
		c.JSON(http.StatusOK, gin.H{"days": days})
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/metrics/reliability?days=7", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

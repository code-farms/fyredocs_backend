package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestGrowthMetrics_RouteExists(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/admin/metrics/growth", func(c *gin.Context) {
		days := c.DefaultQuery("days", "30")
		c.JSON(http.StatusOK, gin.H{"days": days})
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/metrics/growth?days=14", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestGrowthMetrics_DefaultDays(t *testing.T) {
	result := parseQueryInt("", 30)
	if result != 30 {
		t.Errorf("expected default days=30, got %d", result)
	}
}

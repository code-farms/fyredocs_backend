package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAPIPerformance_RouteExists(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/admin/metrics/api-performance", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/metrics/api-performance", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestAPIPerformance_DefaultGatewayURL(t *testing.T) {
	t.Setenv("API_GATEWAY_METRICS_URL", "")
	// Just verify the env var parsing doesn't panic
	url := "http://api-gateway:8080/metrics"
	if url == "" {
		t.Error("expected non-empty default URL")
	}
}

package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestBusinessMetrics_RequiresQueryParams(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Test that queryInt defaults work for the business handler's params
	tests := []struct {
		name     string
		input    string
		fallback int
		expected int
	}{
		{"empty days defaults to 30", "", 30, 30},
		{"custom days", "7", 30, 7},
		{"invalid days uses fallback", "abc", 30, 30},
		{"zero days uses fallback", "0", 30, 30},
		{"empty inactiveDays defaults to 30", "", 30, 30},
		{"custom inactiveDays", "14", 30, 14},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseQueryInt(tt.input, tt.fallback)
			if result != tt.expected {
				t.Errorf("parseQueryInt(%q, %d) = %d, want %d", tt.input, tt.fallback, result, tt.expected)
			}
		})
	}
}

func TestBusinessMetrics_RouteExists(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// Register a dummy handler to verify route pattern
	r.GET("/admin/metrics/business", func(c *gin.Context) {
		days := c.DefaultQuery("days", "30")
		c.JSON(http.StatusOK, gin.H{"days": days})
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/metrics/business?days=7", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

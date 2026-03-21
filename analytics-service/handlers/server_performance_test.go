package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{5 * time.Minute, "5m"},
		{2*time.Hour + 30*time.Minute, "2h 30m"},
		{25*time.Hour + 15*time.Minute, "1d 1h 15m"},
		{48*time.Hour + 5*time.Minute, "2d 0h 5m"},
	}
	for _, tt := range tests {
		got := formatDuration(tt.d)
		if got != tt.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestRoundMB(t *testing.T) {
	got := roundMB(1048576) // 1 MB
	if got != 1.0 {
		t.Errorf("roundMB(1048576) = %f, want 1.0", got)
	}
}

func TestRoundMBf(t *testing.T) {
	got := roundMBf(2097152.0) // 2 MB
	if got != 2.0 {
		t.Errorf("roundMBf(2097152) = %f, want 2.0", got)
	}
}

func TestParseServiceURLs_Default(t *testing.T) {
	t.Setenv("SERVICE_URLS", "")
	urls := parseServiceURLs()
	if len(urls) == 0 {
		t.Error("expected default service URLs, got empty map")
	}
	if _, ok := urls["api-gateway"]; !ok {
		t.Error("expected api-gateway in default URLs")
	}
}

func TestParseServiceURLs_Custom(t *testing.T) {
	t.Setenv("SERVICE_URLS", "svc1=http://localhost:8001,svc2=http://localhost:8002")
	urls := parseServiceURLs()
	if len(urls) != 2 {
		t.Errorf("expected 2 URLs, got %d", len(urls))
	}
	if urls["svc1"] != "http://localhost:8001" {
		t.Errorf("expected svc1=http://localhost:8001, got %q", urls["svc1"])
	}
}

func TestServerPerformance_RouteExists(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/admin/metrics/server-performance", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/metrics/server-performance", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

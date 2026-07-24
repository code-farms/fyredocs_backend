package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestAlertReceiverWiring checks the route wiring: with no DISCORD_WEBHOOK_URL
// the receiver still accepts a Prometheus alert POST and returns 200 (delivery
// logic itself is covered by shared/discord tests).
func TestAlertReceiverWiring(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DISCORD_WEBHOOK_URL", "")

	r := gin.New()
	r.POST("/internal/alerts/api/v2/alerts", AlertReceiver())

	body := `[{"labels":{"alertname":"ServiceDown","severity":"critical"},"annotations":{"summary":"down"}}]`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/internal/alerts/api/v2/alerts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
}

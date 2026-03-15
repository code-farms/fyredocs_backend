package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAuthUserIDNilContext(t *testing.T) {
	result := authUserID(nil)
	if result != nil {
		t.Error("expected nil for nil context")
	}
}

func TestGuestTokenFromHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("X-Guest-Token", "guest-abc-123")

	got := guestToken(c)
	if got != "guest-abc-123" {
		t.Errorf("expected 'guest-abc-123', got %q", got)
	}
}

func TestGuestTokenNoToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	got := guestToken(c)
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

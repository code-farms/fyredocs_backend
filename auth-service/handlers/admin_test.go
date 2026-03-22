package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRevokeUserSessionsInvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ae := &AuthEndpoints{}
	rec := httptest.NewRecorder()
	c, r := gin.CreateTestContext(rec)
	r.POST("/internal/users/:id/revoke-sessions", ae.RevokeUserSessions)
	c.Request = httptest.NewRequest(http.MethodPost, "/internal/users/not-a-uuid/revoke-sessions", nil)
	r.ServeHTTP(rec, c.Request)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestRevokeSessionInvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ae := &AuthEndpoints{}
	rec := httptest.NewRecorder()
	c, r := gin.CreateTestContext(rec)
	r.DELETE("/internal/sessions/:id", ae.RevokeSession)
	c.Request = httptest.NewRequest(http.MethodDelete, "/internal/sessions/not-a-uuid", nil)
	r.ServeHTTP(rec, c.Request)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

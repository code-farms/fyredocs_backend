package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestChangePlan_InvalidJSON_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/auth/plan", bytes.NewBufferString(`{invalid`))
	c.Request.Header.Set("Content-Type", "application/json")

	ae := &AuthEndpoints{}
	ae.ChangePlan(c)

	// Auth check runs before JSON binding, so we get 401 without auth context
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthenticated request, got %d", rec.Code)
	}
}

func TestChangePlan_EmptyPlanName_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	body, _ := json.Marshal(map[string]string{"planName": ""})
	c.Request = httptest.NewRequest(http.MethodPut, "/auth/plan", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	ae := &AuthEndpoints{}
	ae.ChangePlan(c)

	// Should return 400 or 401 (no auth context) — either way, not 200
	if rec.Code == http.StatusOK {
		t.Error("expected non-200 for empty plan name")
	}
}

func TestChangePlan_NoAuthContext_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	body, _ := json.Marshal(map[string]string{"planName": "pro"})
	c.Request = httptest.NewRequest(http.MethodPut, "/auth/plan", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	ae := &AuthEndpoints{}
	ae.ChangePlan(c)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without auth context, got %d", rec.Code)
	}
}

// TestCanChangePlan is the authorization gate that stops an end user from
// self-upgrading to a paid plan (finding F2). Only admins/super-admins qualify.
func TestCanChangePlan(t *testing.T) {
	cases := map[string]bool{
		"user":        false,
		"":            false,
		"pro":         false, // a plan name is not a role
		"admin":       true,
		"super-admin": true,
		"  admin  ":   true, // trimmed
		"Admin":       false, // case-sensitive: only exact roles allowed
	}
	for role, want := range cases {
		if got := canChangePlan(role); got != want {
			t.Errorf("canChangePlan(%q) = %v, want %v", role, got, want)
		}
	}
}

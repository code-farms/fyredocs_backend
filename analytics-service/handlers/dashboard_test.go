package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// These cover the role-routing guards that return before any DB access. The
// admin/user branches require a database and are exercised by the end-to-end
// verification, consistent with the other analytics handler tests.

func dashboardRequest(headers map[string]string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, r := gin.CreateTestContext(rec)
	r.GET("/api/dashboard", Dashboard)
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	c.Request = req
	r.ServeHTTP(rec, req)
	return rec
}

func TestDashboardUnauthenticated(t *testing.T) {
	rec := dashboardRequest(nil) // no X-User-ID
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without X-User-ID, got %d", rec.Code)
	}
}

func TestDashboardGuestForbidden(t *testing.T) {
	rec := dashboardRequest(map[string]string{
		"X-User-ID":   "11111111-1111-1111-1111-111111111111",
		"X-User-Role": "guest",
	})
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for guest, got %d", rec.Code)
	}
}

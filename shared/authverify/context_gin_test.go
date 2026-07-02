package authverify

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSetGetGinAuthRoundTrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	want := AuthContext{UserID: "u1", Role: "user", Plan: "pro", PlanMaxFileSizeMB: 500}
	SetGinAuth(c, want)

	got, ok := GetGinAuth(c)
	if !ok {
		t.Fatal("GetGinAuth returned ok=false after SetGinAuth")
	}
	if got.UserID != want.UserID || got.Role != want.Role || got.Plan != want.Plan || got.PlanMaxFileSizeMB != want.PlanMaxFileSizeMB {
		t.Errorf("round-trip = %+v, want %+v", got, want)
	}
}

func TestGetGinAuthRequestContextFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	// Auth set only on the request context (e.g. by non-Gin middleware).
	want := AuthContext{UserID: "u2", Role: "guest", IsGuest: true}
	c.Request = SetRequestAuth(req, want)

	got, ok := GetGinAuth(c)
	if !ok || got.UserID != "u2" || !got.IsGuest {
		t.Errorf("fallback = %+v (ok=%v), want request-context auth", got, ok)
	}
}

func TestGinAuthNilSafety(t *testing.T) {
	SetGinAuth(nil, AuthContext{UserID: "u1"}) // must not panic
	if _, ok := GetGinAuth(nil); ok {
		t.Error("GetGinAuth(nil) must return ok=false")
	}
}

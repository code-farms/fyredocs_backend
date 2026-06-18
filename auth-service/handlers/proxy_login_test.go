package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"auth-service/internal/authverify"
)

// newProxyLoginRouter builds a gin engine with the ProxyLogin route, optionally
// injecting an authenticated caller into the gin context via SetGinAuth.
func newProxyLoginRouter(caller *authverify.AuthContext) (*gin.Engine, *AuthEndpoints) {
	gin.SetMode(gin.TestMode)
	ae := &AuthEndpoints{}
	r := gin.New()
	if caller != nil {
		c := *caller
		r.Use(func(ctx *gin.Context) {
			authverify.SetGinAuth(ctx, c)
			ctx.Next()
		})
	}
	r.POST("/auth/proxy-login", ae.ProxyLogin)
	return r, ae
}

func doProxyLogin(r *gin.Engine, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/proxy-login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	return rec
}

func TestProxyLoginDisabled(t *testing.T) {
	t.Setenv("PROXY_LOGIN_ENABLED", "false")
	r, _ := newProxyLoginRouter(&authverify.AuthContext{UserID: "11111111-1111-1111-1111-111111111111", Role: "super-admin"})
	rec := doProxyLogin(r, `{"userId":"22222222-2222-2222-2222-222222222222"}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 when disabled, got %d", rec.Code)
	}
}

func TestProxyLoginNoAuth(t *testing.T) {
	t.Setenv("PROXY_LOGIN_ENABLED", "true")
	r, _ := newProxyLoginRouter(nil)
	rec := doProxyLogin(r, `{"userId":"22222222-2222-2222-2222-222222222222"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without auth context, got %d", rec.Code)
	}
}

func TestProxyLoginNonAdminForbidden(t *testing.T) {
	t.Setenv("PROXY_LOGIN_ENABLED", "true")
	r, _ := newProxyLoginRouter(&authverify.AuthContext{UserID: "11111111-1111-1111-1111-111111111111", Role: "user"})
	rec := doProxyLogin(r, `{"userId":"22222222-2222-2222-2222-222222222222"}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for non-admin caller, got %d", rec.Code)
	}
}

func TestProxyLoginInvalidBody(t *testing.T) {
	t.Setenv("PROXY_LOGIN_ENABLED", "true")
	r, _ := newProxyLoginRouter(&authverify.AuthContext{UserID: "11111111-1111-1111-1111-111111111111", Role: "admin"})
	rec := doProxyLogin(r, `not-json`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid body, got %d", rec.Code)
	}
}

func TestProxyLoginInvalidTargetID(t *testing.T) {
	t.Setenv("PROXY_LOGIN_ENABLED", "true")
	r, _ := newProxyLoginRouter(&authverify.AuthContext{UserID: "11111111-1111-1111-1111-111111111111", Role: "admin"})
	rec := doProxyLogin(r, `{"userId":"not-a-uuid"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid target ID, got %d", rec.Code)
	}
}

func TestProxyLoginSelfForbidden(t *testing.T) {
	t.Setenv("PROXY_LOGIN_ENABLED", "true")
	const adminID = "11111111-1111-1111-1111-111111111111"
	r, _ := newProxyLoginRouter(&authverify.AuthContext{UserID: adminID, Role: "super-admin"})
	rec := doProxyLogin(r, `{"userId":"`+adminID+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for self-impersonation, got %d", rec.Code)
	}
}

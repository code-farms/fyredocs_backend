// Tests for the impersonation endpoint's pre-DB guards. The invariant under
// guard: only exact "admin"/"super-admin" role claims pass the fast-path gate,
// self-impersonation is refused, and the endpoint can be disabled outright —
// all before any database access.
package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"fyredocs/shared/authverify"
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

// TestProxyLoginRoleGate is the full matrix for the fast-path role gate
// (proxyLoginAllowedRoles). Denied roles must 403 on a perfectly valid request,
// proving the denial comes from the role alone. Allowed roles are proven to
// pass the gate by sending an unparseable body: the handler then stops at
// binding with a 400 — before it can reach the nil models.DB, which would
// panic — so no database is needed to observe the gate's decision.
func TestProxyLoginRoleGate(t *testing.T) {
	t.Setenv("PROXY_LOGIN_ENABLED", "true")
	const callerID = "11111111-1111-1111-1111-111111111111"
	const validBody = `{"userId":"22222222-2222-2222-2222-222222222222"}`

	// "Admin"/"ADMIN" pin case sensitivity; "superadmin" pins the exact spelling
	// (the hyphen is load-bearing); "guest" and "" pin the unauthenticated-ish
	// claims a token could plausibly carry.
	denied := []string{"", "guest", "user", "Admin", "ADMIN", "superadmin", "Super-Admin", "root"}
	for _, role := range denied {
		t.Run(fmt.Sprintf("denied role %q", role), func(t *testing.T) {
			r, _ := newProxyLoginRouter(&authverify.AuthContext{UserID: callerID, Role: role})
			rec := doProxyLogin(r, validBody)
			if rec.Code != http.StatusForbidden {
				t.Errorf("expected 403 for role %q, got %d", role, rec.Code)
			}
		})
	}

	// Leading/trailing whitespace is trimmed before the map lookup, so a padded
	// admin claim still passes the gate.
	allowed := []string{"admin", "super-admin", "  admin  "}
	for _, role := range allowed {
		t.Run(fmt.Sprintf("allowed role %q passes gate", role), func(t *testing.T) {
			r, _ := newProxyLoginRouter(&authverify.AuthContext{UserID: callerID, Role: role})
			rec := doProxyLogin(r, `not-json`)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected 400 (past role gate, stopped at binding) for role %q, got %d", role, rec.Code)
			}
		})
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

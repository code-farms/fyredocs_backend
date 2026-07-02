package authverify

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestIsAuthenticated(t *testing.T) {
	if !IsAuthenticated(AuthContext{UserID: "u1"}) {
		t.Error("user with ID should be authenticated")
	}
	if IsAuthenticated(AuthContext{UserID: "u1", IsGuest: true}) {
		t.Error("guest must not count as authenticated")
	}
	if IsAuthenticated(AuthContext{}) {
		t.Error("empty context must not be authenticated")
	}
	if IsAuthenticated(AuthContext{UserID: "   "}) {
		t.Error("whitespace user ID must not be authenticated")
	}
}

func TestHasRole(t *testing.T) {
	if !HasRole(AuthContext{Role: "admin"}, "editor") {
		t.Error("admin must bypass any role requirement")
	}
	if !HasRole(AuthContext{Role: "editor"}, "editor") {
		t.Error("exact role match must pass")
	}
	if HasRole(AuthContext{Role: "viewer"}, "editor") {
		t.Error("non-matching role must fail")
	}
	if HasRole(AuthContext{}, "editor") {
		t.Error("empty role must fail")
	}
}

func TestHasScope(t *testing.T) {
	if !HasScope(AuthContext{Role: "admin"}, "jobs:write") {
		t.Error("admin must bypass scope checks")
	}
	if !HasScope(AuthContext{Scope: []string{"jobs:read", "jobs:write"}}, "jobs:write") {
		t.Error("granted scope must pass")
	}
	if HasScope(AuthContext{Scope: []string{"jobs:read"}}, "jobs:write") {
		t.Error("missing scope must fail")
	}
	if HasScope(AuthContext{}, "jobs:write") {
		t.Error("empty scope list must fail")
	}
}

func TestSecureEqual(t *testing.T) {
	if secureEqual("", "x") || secureEqual("x", "") || secureEqual("", "") {
		t.Error("empty operands must never compare equal")
	}
	if !secureEqual("editor", "editor") {
		t.Error("identical strings must compare equal")
	}
	if secureEqual("editor", "edit") {
		t.Error("different-length strings must not compare equal")
	}
}

func requireHarness(mw gin.HandlerFunc, authCtx *AuthContext) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if authCtx != nil {
		ctx := *authCtx
		r.Use(func(c *gin.Context) { SetGinAuth(c, ctx); c.Next() })
	}
	r.Use(mw)
	r.GET("/probe", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/probe", nil))
	return w
}

func TestRequireAuthenticatedGin(t *testing.T) {
	if w := requireHarness(RequireAuthenticatedGin(), nil); w.Code != http.StatusUnauthorized {
		t.Errorf("no auth: status = %d, want 401", w.Code)
	}
	guest := AuthContext{UserID: "g1", IsGuest: true}
	if w := requireHarness(RequireAuthenticatedGin(), &guest); w.Code != http.StatusForbidden {
		t.Errorf("guest: status = %d, want 403", w.Code)
	}
	user := AuthContext{UserID: "u1", Role: "user"}
	if w := requireHarness(RequireAuthenticatedGin(), &user); w.Code != http.StatusOK {
		t.Errorf("user: status = %d, want 200", w.Code)
	}
}

func TestRequireRoleGin(t *testing.T) {
	viewer := AuthContext{UserID: "u1", Role: "viewer"}
	if w := requireHarness(RequireRoleGin("editor"), &viewer); w.Code != http.StatusForbidden {
		t.Errorf("viewer needing editor: status = %d, want 403", w.Code)
	}
	admin := AuthContext{UserID: "u2", Role: "admin"}
	if w := requireHarness(RequireRoleGin("editor"), &admin); w.Code != http.StatusOK {
		t.Errorf("admin: status = %d, want 200 (admin override)", w.Code)
	}
}

func TestRequireScopeGin(t *testing.T) {
	noScope := AuthContext{UserID: "u1", Role: "user"}
	if w := requireHarness(RequireScopeGin("jobs:write"), &noScope); w.Code != http.StatusForbidden {
		t.Errorf("no scope: status = %d, want 403", w.Code)
	}
	scoped := AuthContext{UserID: "u2", Role: "user", Scope: []string{"jobs:write"}}
	if w := requireHarness(RequireScopeGin("jobs:write"), &scoped); w.Code != http.StatusOK {
		t.Errorf("scoped: status = %d, want 200", w.Code)
	}
}

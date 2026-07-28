package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"fyredocs/shared/authverify"
)

func TestParseQueryInt(t *testing.T) {
	cases := []struct {
		in       string
		fallback int
		want     int
	}{
		{"", 25, 25},
		{"10", 25, 10},
		{"abc", 25, 25},
		{"0", 25, 25},
		{"-5", 25, 25},
	}
	for _, c := range cases {
		if got := parseQueryInt(c.in, c.fallback); got != c.want {
			t.Errorf("parseQueryInt(%q,%d)=%d want %d", c.in, c.fallback, got, c.want)
		}
	}
}

func TestParseUUID(t *testing.T) {
	if v, ok := parseUUID(nil); !ok || v != nil {
		t.Error("nil pointer should be valid with nil value")
	}
	empty := "  "
	if v, ok := parseUUID(&empty); !ok || v != nil {
		t.Error("blank string should be valid with nil value")
	}
	valid := uuid.Must(uuid.NewV7()).String()
	if v, ok := parseUUID(&valid); !ok || v == nil {
		t.Error("valid uuid should parse")
	}
	bad := "not-a-uuid"
	if _, ok := parseUUID(&bad); ok {
		t.Error("invalid uuid should not be ok")
	}
}

// serveWithAuth runs a RequireUser-guarded route, optionally seeding the
// AuthContext the way the auth middleware would after verifying a JWT. Passing a
// nil authCtx models an unauthenticated request that reached the handler chain.
func serveWithAuth(authCtx *authverify.AuthContext, mutate func(*http.Request)) int {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if authCtx != nil {
		r.Use(func(c *gin.Context) {
			authverify.SetGinAuth(c, *authCtx)
			c.Next()
		})
	}
	r.Use(RequireUser())
	r.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if mutate != nil {
		mutate(req)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

// TestRequireUserRejectsForgedHeader is the regression guard for the multi-host
// split: identity must come from the verified AuthContext, never from the raw
// X-User-ID header. Before local JWT verification this header alone granted
// access, so a service reachable from anything but the gateway could be
// impersonated. If this test starts passing a 200, that hole is back open.
func TestRequireUserRejectsForgedHeader(t *testing.T) {
	forged := uuid.Must(uuid.NewV7()).String()
	code := serveWithAuth(nil, func(req *http.Request) {
		req.Header.Set("X-User-ID", forged)
	})
	if code != http.StatusUnauthorized {
		t.Errorf("forged X-User-ID with no verified identity: expected 401, got %d", code)
	}
}

func TestRequireUser(t *testing.T) {
	// No identity at all → 401.
	if code := serveWithAuth(nil, nil); code != http.StatusUnauthorized {
		t.Errorf("no identity: expected 401, got %d", code)
	}

	// Verified identity → 200.
	valid := authverify.AuthContext{UserID: uuid.Must(uuid.NewV7()).String()}
	if code := serveWithAuth(&valid, nil); code != http.StatusOK {
		t.Errorf("verified identity: expected 200, got %d", code)
	}

	// Verified context carrying a non-UUID subject → 401. The token was valid but
	// its subject cannot name a document owner, so it must not authorize.
	if code := serveWithAuth(&authverify.AuthContext{UserID: "garbage"}, nil); code != http.StatusUnauthorized {
		t.Errorf("malformed subject: expected 401, got %d", code)
	}

	// Empty UserID (e.g. a guest context) → 401.
	guest := authverify.AuthContext{Role: "guest", IsGuest: true}
	if code := serveWithAuth(&guest, nil); code != http.StatusUnauthorized {
		t.Errorf("guest identity: expected 401, got %d", code)
	}
}

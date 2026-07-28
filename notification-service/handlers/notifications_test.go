package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"fyredocs/shared/authverify"
)

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
// access, so a service reachable from anything but the gateway could have another
// user's notification feed read out of it.
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
	if code := serveWithAuth(nil, nil); code != http.StatusUnauthorized {
		t.Errorf("no identity: expected 401, got %d", code)
	}

	valid := authverify.AuthContext{UserID: uuid.Must(uuid.NewV7()).String()}
	if code := serveWithAuth(&valid, nil); code != http.StatusOK {
		t.Errorf("verified identity: expected 200, got %d", code)
	}

	if code := serveWithAuth(&authverify.AuthContext{UserID: "garbage"}, nil); code != http.StatusUnauthorized {
		t.Errorf("malformed subject: expected 401, got %d", code)
	}

	guest := authverify.AuthContext{Role: "guest", IsGuest: true}
	if code := serveWithAuth(&guest, nil); code != http.StatusUnauthorized {
		t.Errorf("guest identity: expected 401, got %d", code)
	}
}

func TestNotificationRoutePatterns(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	n := r.Group("/api/notifications")
	ok := func(c *gin.Context) { c.Status(http.StatusOK) }
	n.GET("", ok)
	n.POST("/read-all", ok)
	n.POST("/:id/read", ok)

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/notifications"},
		{http.MethodPost, "/api/notifications/read-all"},
		{http.MethodPost, "/api/notifications/abc/read"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("%s %s: expected 200, got %d", tc.method, tc.path, w.Code)
		}
	}
}

package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"fyredocs/shared/authverify"
	"fyredocs/shared/redisstore"
)

// These cover the role-routing guards that return before any DB access. The
// admin/user branches require a database and are exercised by the end-to-end
// verification, consistent with the other analytics handler tests.

// dashboardRequest drives the handler with an optional seeded AuthContext,
// standing in for the auth middleware that main.go installs. A nil authCtx models
// a request carrying no verifiable credentials. headers are still settable so
// tests can assert that raw X-User-* headers do NOT influence the outcome.
func dashboardRequest(authCtx *authverify.AuthContext, headers map[string]string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	_, r := gin.CreateTestContext(rec)
	if authCtx != nil {
		r.Use(func(c *gin.Context) {
			authverify.SetGinAuth(c, *authCtx)
			c.Next()
		})
	}
	r.GET("/api/dashboard", Dashboard)
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	r.ServeHTTP(rec, req)
	return rec
}

func TestDashboardUnauthenticated(t *testing.T) {
	rec := dashboardRequest(nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without a verified identity, got %d", rec.Code)
	}
}

// TestDashboardForgedHeadersRejected is the regression guard for the multi-host
// split: the handler once read X-User-ID/X-User-Role straight off the request, so
// a caller reaching this service directly could claim an admin role and receive
// system-wide KPIs. Identity now comes only from the verified AuthContext.
func TestDashboardForgedHeadersRejected(t *testing.T) {
	rec := dashboardRequest(nil, map[string]string{
		"X-User-ID":   "11111111-1111-1111-1111-111111111111",
		"X-User-Role": "super-admin",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for forged admin headers, got %d", rec.Code)
	}
}

func TestDashboardGuestForbidden(t *testing.T) {
	rec := dashboardRequest(&authverify.AuthContext{
		UserID:  "11111111-1111-1111-1111-111111111111",
		Role:    "guest",
		IsGuest: true,
	}, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for guest, got %d", rec.Code)
	}
}

func TestDashboardCacheKey(t *testing.T) {
	const uid = "11111111-1111-1111-1111-111111111111"
	cases := []struct {
		role, userID string
		days         int
		want         string
	}{
		{"user", uid, 30, "cache:dashboard:v1:user:" + uid + ":d30"},
		{"", uid, 7, "cache:dashboard:v1:user:" + uid + ":d7"},
		{"admin", uid, 30, "cache:dashboard:v1:admin:d30"},
		{"super-admin", uid, 90, "cache:dashboard:v1:admin:d90"},
	}
	for _, c := range cases {
		if got := dashboardCacheKey(c.role, c.userID, c.days); got != c.want {
			t.Errorf("dashboardCacheKey(%q,%q,%d) = %q, want %q", c.role, c.userID, c.days, got, c.want)
		}
	}
}

// On a cache hit the handler must serve the stored payload verbatim without ever
// touching the database (models.DB is nil here — if the handler tried to query,
// it would panic).
func TestDashboardServesFromCacheWithoutDB(t *testing.T) {
	const uid = "11111111-1111-1111-1111-111111111111"

	mr := miniredis.RunT(t)
	prevClient, prevTTL := redisstore.Client, dashboardCacheTTL
	redisstore.Client = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	dashboardCacheTTL = 30 * time.Second
	t.Cleanup(func() {
		redisstore.Client.Close()
		redisstore.Client, dashboardCacheTTL = prevClient, prevTTL
	})

	// Seed the exact key the handler will compute (role "user", days default 30).
	const sentinel = "cached-sentinel-payload"
	cached := `{"role":"user","marker":"` + sentinel + `"}`
	if err := redisstore.Client.Set(context.Background(), dashboardCacheKey("user", uid, 30), cached, time.Minute).Err(); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	rec := dashboardRequest(&authverify.AuthContext{UserID: uid, Role: "user"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on cache hit, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), sentinel) {
		t.Errorf("response did not contain cached payload; body=%s", rec.Body.String())
	}
}

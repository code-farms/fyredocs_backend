package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"fyredocs/shared/authverify"
)

func TestLimitForPlan(t *testing.T) {
	cfg := Config{AnonLimit: 10, FreeLimit: 50, ProLimit: 200}
	cases := map[string]int{
		"pro":       200,
		"PRO":       200,
		"free":      50,
		" Free ":    50,
		"anonymous": 10,
		"":          10,
		"garbage":   10,
	}
	for plan, want := range cases {
		if got := cfg.limitForPlan(plan); got != want {
			t.Errorf("limitForPlan(%q) = %d, want %d", plan, got, want)
		}
	}
}

func TestShouldLimit(t *testing.T) {
	limited := []string{"/api/jobs", "/api/upload/init", "/api"}
	notLimited := []string{"/auth/login", "/metrics", "/healthz", "/", "/assets/app.js", "/fyredocs-uploads/x"}
	for _, p := range limited {
		if !shouldLimit(p) {
			t.Errorf("shouldLimit(%q) = false, want true", p)
		}
	}
	for _, p := range notLimited {
		if shouldLimit(p) {
			t.Errorf("shouldLimit(%q) = true, want false", p)
		}
	}
}

func TestIdentity(t *testing.T) {
	// Authenticated, non-guest user → keyed by user ID and its plan.
	subject, plan := identity(authverify.AuthContext{UserID: "u1", Plan: "pro"}, true, "1.2.3.4")
	if subject != "user:u1" || plan != "pro" {
		t.Errorf("authed identity = (%q,%q), want (user:u1,pro)", subject, plan)
	}
	// Guest with a cached plan → still anon, keyed by IP.
	subject, plan = identity(authverify.AuthContext{UserID: "g1", Plan: "pro", IsGuest: true}, true, "1.2.3.4")
	if subject != "ip:1.2.3.4" || plan != "" {
		t.Errorf("guest identity = (%q,%q), want (ip:1.2.3.4,\"\")", subject, plan)
	}
	// No auth context → keyed by IP.
	subject, plan = identity(authverify.AuthContext{}, false, "5.6.7.8")
	if subject != "ip:5.6.7.8" {
		t.Errorf("anon identity subject = %q, want ip:5.6.7.8", subject)
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	r.RemoteAddr = "10.0.0.1:5555"
	if got := clientIP(r); got != "10.0.0.1" {
		t.Errorf("clientIP(RemoteAddr) = %q, want 10.0.0.1", got)
	}
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	if got := clientIP(r); got != "203.0.113.9" {
		t.Errorf("clientIP(XFF) = %q, want 203.0.113.9", got)
	}
}

// newTestMiddleware wires the limiter against an in-process miniredis and an
// authenticated request context for the given plan.
func newTestMiddleware(t *testing.T, cfg Config) (http.Handler, *int) {
	t.Helper()
	hits := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	})
	return Middleware(cfg)(next), &hits
}

func TestMiddlewareNilClientFailsOpen(t *testing.T) {
	h, hits := newTestMiddleware(t, Config{Client: nil})
	for i := 0; i < 100; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("nil client should fail open, got %d", rec.Code)
		}
	}
	if *hits != 100 {
		t.Fatalf("expected 100 passthroughs, got %d", *hits)
	}
}

func TestMiddlewareSkipsNonAPIRoutes(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	h, hits := newTestMiddleware(t, Config{Client: client, Window: time.Minute, AnonLimit: 1})
	// /auth and SPA paths bypass the limiter even when way over the anon limit.
	for _, p := range []string{"/auth/login", "/", "/metrics"} {
		for i := 0; i < 5; i++ {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, p, nil)
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("path %q should bypass limiter, got %d", p, rec.Code)
			}
		}
	}
	if *hits != 15 {
		t.Fatalf("expected 15 passthroughs, got %d", *hits)
	}
}

func TestMiddlewareEnforcesAnonLimitByIP(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	h, hits := newTestMiddleware(t, Config{Client: client, Window: time.Minute, AnonLimit: 3})

	do := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
		req.RemoteAddr = "9.9.9.9:1234"
		h.ServeHTTP(rec, req)
		return rec
	}

	for i := 0; i < 3; i++ {
		if rec := do(); rec.Code != http.StatusOK {
			t.Fatalf("request %d should be allowed, got %d", i+1, rec.Code)
		}
	}
	rec := do() // 4th request exceeds the limit of 3
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("4th request should be 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("429 response missing Retry-After header")
	}
	if rec.Header().Get("X-RateLimit-Limit") != "3" {
		t.Errorf("X-RateLimit-Limit = %q, want 3", rec.Header().Get("X-RateLimit-Limit"))
	}
	if *hits != 3 {
		t.Fatalf("expected 3 passthroughs before block, got %d", *hits)
	}
}

func TestMiddlewareProPlanGetsHigherCeiling(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	h, _ := newTestMiddleware(t, Config{Client: client, Window: time.Minute, AnonLimit: 1, FreeLimit: 2, ProLimit: 5})

	proCtx := authverify.AuthContext{UserID: "pro-user", Plan: "pro"}
	do := func() int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
		req = req.WithContext(authverify.WithAuthContext(req.Context(), proCtx))
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := 0; i < 5; i++ {
		if code := do(); code != http.StatusOK {
			t.Fatalf("pro request %d should be allowed (ceiling 5), got %d", i+1, code)
		}
	}
	if code := do(); code != http.StatusTooManyRequests {
		t.Fatalf("6th pro request should be 429, got %d", code)
	}
}

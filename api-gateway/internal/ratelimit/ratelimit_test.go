package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"fyredocs/shared/authverify"
)

func TestLimitForPlan(t *testing.T) {
	cfg := Config{GuestLimit: 10, FreeLimit: 50, ProLimit: 200}
	cases := map[string]int{
		"pro":     200,
		"PRO":     200,
		"free":    50,
		" Free ":  50,
		"guest":   10,
		"":        10,
		"garbage": 10,
	}
	for plan, want := range cases {
		if got := cfg.limitForPlan(plan); got != want {
			t.Errorf("limitForPlan(%q) = %d, want %d", plan, got, want)
		}
	}
}

func TestShouldLimit(t *testing.T) {
	limited := []string{"/api/jobs", "/api/upload/init", "/api"}
	notLimited := []string{"/auth/login", "/metrics", "/healthz", "/", "/assets/app.js", "/uploads/x"}
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
	// Authenticated context with a blank/whitespace user ID must NOT mint a
	// shared "user:" bucket that every such caller would drain together — it
	// falls back to per-IP keying with the guest plan.
	subject, plan = identity(authverify.AuthContext{UserID: "   ", Plan: "pro"}, true, "5.6.7.8")
	if subject != "ip:5.6.7.8" || plan != "" {
		t.Errorf("blank-user identity = (%q,%q), want (ip:5.6.7.8,\"\")", subject, plan)
	}
}

func TestRemoteAddrIPFallback(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	r.RemoteAddr = "10.0.0.1:5555"
	if got := remoteAddrIP(r); got != "10.0.0.1" {
		t.Errorf("remoteAddrIP(RemoteAddr) = %q, want 10.0.0.1", got)
	}
	// The fallback resolver must NEVER honor X-Forwarded-For — it is
	// client-supplied. Trusted-proxy XFF resolution lives in shared/clientip
	// and is injected via Config.ClientIP.
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	if got := remoteAddrIP(r); got != "10.0.0.1" {
		t.Errorf("remoteAddrIP must ignore XFF, got %q", got)
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

	h, hits := newTestMiddleware(t, Config{Client: client, Window: time.Minute, GuestLimit: 1})
	// /auth and SPA paths bypass the limiter even when way over the guest limit.
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

func TestMiddlewareEnforcesGuestLimitByIP(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	h, hits := newTestMiddleware(t, Config{Client: client, Window: time.Minute, GuestLimit: 3})

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

// TestMiddlewareEmitsRateLimitHeaders pins the client-facing throttling
// contract: every limited (allowed or blocked) response carries
// X-RateLimit-Limit/-Remaining/-Reset, Remaining counts down per request, and
// Retry-After appears ONLY on the 429 — a client backoff loop keys off these
// exact headers.
func TestMiddlewareEmitsRateLimitHeaders(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	h, _ := newTestMiddleware(t, Config{Client: client, Window: time.Minute, GuestLimit: 2})

	do := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
		req.RemoteAddr = "7.7.7.7:1234"
		h.ServeHTTP(rec, req)
		return rec
	}

	t.Run("allowed requests carry headers, remaining counts down, no Retry-After", func(t *testing.T) {
		wantRemaining := []string{"1", "0"}
		for i, want := range wantRemaining {
			rec := do()
			if rec.Code != http.StatusOK {
				t.Fatalf("request %d: status = %d, want 200", i+1, rec.Code)
			}
			if got := rec.Header().Get("X-RateLimit-Limit"); got != "2" {
				t.Errorf("request %d: X-RateLimit-Limit = %q, want 2", i+1, got)
			}
			if got := rec.Header().Get("X-RateLimit-Remaining"); got != want {
				t.Errorf("request %d: X-RateLimit-Remaining = %q, want %q", i+1, got, want)
			}
			reset, err := strconv.ParseInt(rec.Header().Get("X-RateLimit-Reset"), 10, 64)
			if err != nil || reset <= time.Now().Unix() {
				t.Errorf("request %d: X-RateLimit-Reset = %q, want a future unix timestamp",
					i+1, rec.Header().Get("X-RateLimit-Reset"))
			}
			if got := rec.Header().Get("Retry-After"); got != "" {
				t.Errorf("request %d: allowed response must not carry Retry-After, got %q", i+1, got)
			}
		}
	})

	t.Run("blocked request carries Retry-After and zero remaining", func(t *testing.T) {
		rec := do()
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429", rec.Code)
		}
		// Retry-After is the full window in seconds (ratelimit.go: the limiter
		// does not compute the oldest entry's expiry, it advertises the window).
		if got := rec.Header().Get("Retry-After"); got != "60" {
			t.Errorf("Retry-After = %q, want 60 (window seconds)", got)
		}
		if got := rec.Header().Get("X-RateLimit-Remaining"); got != "0" {
			t.Errorf("X-RateLimit-Remaining = %q, want 0", got)
		}
		if got := rec.Header().Get("X-RateLimit-Limit"); got != "2" {
			t.Errorf("X-RateLimit-Limit = %q, want 2", got)
		}
	})
}

// TestMiddlewareUnknownPlanFallsBackToGuestCeiling pins the fallback direction
// for an unrecognized plan string: it gets the GUEST ceiling, not free or pro
// (ratelimit.go limitForPlan's default arm). This is the fail-safe choice — a
// typo'd or newly-added plan name that the limiter doesn't know yet is
// throttled hardest rather than granted the widest ceiling.
func TestMiddlewareUnknownPlanFallsBackToGuestCeiling(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	h, _ := newTestMiddleware(t, Config{Client: client, Window: time.Minute, GuestLimit: 2, FreeLimit: 10, ProLimit: 20})

	entCtx := authverify.AuthContext{UserID: "ent-user", Plan: "enterprise"}
	do := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
		req = req.WithContext(authverify.WithAuthContext(req.Context(), entCtx))
		h.ServeHTTP(rec, req)
		return rec
	}

	for i := 0; i < 2; i++ {
		if rec := do(); rec.Code != http.StatusOK {
			t.Fatalf("request %d should be allowed at the guest ceiling, got %d", i+1, rec.Code)
		}
	}
	rec := do()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd request with unknown plan should hit the guest ceiling (2), got %d", rec.Code)
	}
	if got := rec.Header().Get("X-RateLimit-Limit"); got != "2" {
		t.Errorf("X-RateLimit-Limit = %q, want 2 (guest ceiling, not free/pro)", got)
	}
}

// TestMiddlewareRedisErrorFailsOpen pins the outage posture with a client that
// ERRORS (not the nil-client shortcut TestMiddlewareNilClientFailsOpen covers):
// killing miniredis mid-run makes every subsequent limiter call fail, and those
// requests must still reach the handler. A fail-closed limiter would convert a
// Redis blip into a full API outage. The fail-open responses carry no
// X-RateLimit headers — there is no window state to report.
func TestMiddlewareRedisErrorFailsOpen(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	h, hits := newTestMiddleware(t, Config{Client: client, Window: time.Minute, GuestLimit: 1})

	do := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
		req.RemoteAddr = "8.8.8.8:1234"
		h.ServeHTTP(rec, req)
		return rec
	}

	// Sanity: with Redis up, the limiter is actually limiting (2nd hits 429).
	if rec := do(); rec.Code != http.StatusOK {
		t.Fatalf("first request should pass, got %d", rec.Code)
	}
	if rec := do(); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request should be 429 while Redis is up, got %d", rec.Code)
	}

	mr.Close()

	// Way past the ceiling: every request must fail open once Redis errors.
	for i := 0; i < 5; i++ {
		rec := do()
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d after Redis death: status = %d, want 200 (fail open)", i+1, rec.Code)
		}
		if got := rec.Header().Get("X-RateLimit-Limit"); got != "" {
			t.Errorf("fail-open response carries X-RateLimit-Limit = %q, want none", got)
		}
	}
	if *hits != 6 {
		t.Fatalf("handler hits = %d, want 6 (1 allowed + 5 fail-open)", *hits)
	}
}

func TestMiddlewareProPlanGetsHigherCeiling(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	h, _ := newTestMiddleware(t, Config{Client: client, Window: time.Minute, GuestLimit: 1, FreeLimit: 2, ProLimit: 5})

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

// BenchmarkRateLimitMiddleware guards the per-request limiter overhead on the
// gateway hot path (identity resolution + sliding-window Redis round trip).
// miniredis keeps the store in-process, so the number measures our middleware
// cost rather than network latency; the huge limit keeps every iteration on
// the allowed path, which is the path production requests take.
func BenchmarkRateLimitMiddleware(b *testing.B) {
	mr := miniredis.RunT(b)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := Middleware(Config{Client: client, Window: time.Minute, GuestLimit: 1 << 30})(next)

	req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	b.ReportAllocs()
	for b.Loop() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d, want 200", rec.Code)
		}
	}
}

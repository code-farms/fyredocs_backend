// Package ratelimit provides a per-plan, Redis-backed sliding-window rate
// limiter for the api-gateway's authenticated /api/* routes.
//
// It is intentionally implemented here (net/http) rather than imported from
// auth-service: per the project microservice rules each service owns its own
// middleware and there is no shared business logic. The sliding-window ZSET
// algorithm mirrors auth-service's endpoint limiter but keys by plan + identity
// instead of by raw IP, so paid plans get higher ceilings than guests.
package ratelimit

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"fyredocs/shared/authverify"
	"fyredocs/shared/response"
)

const defaultKeyPrefix = "apilimit"

// Config configures the API rate limiter.
type Config struct {
	// Client is the shared gateway Redis client. When nil the limiter
	// fails open (allows every request) — matching the gateway's posture of
	// never letting a Redis outage take down request serving.
	Client *redis.Client
	// Window is the sliding-window duration (e.g. 1 minute).
	Window time.Duration
	// Per-plan request ceilings within Window.
	AnonLimit int
	FreeLimit int
	ProLimit  int
	// KeyPrefix namespaces the Redis keys; defaults to "apilimit".
	KeyPrefix string
}

// slidingWindowLua atomically trims the window, counts current entries, records
// this request, and refreshes the key TTL. Returns the pre-insert count.
var slidingWindowLua = redis.NewScript(`
local key = KEYS[1]
local window_start = tonumber(ARGV[1])
local now = tonumber(ARGV[2])
local member = ARGV[3]
local ttl = tonumber(ARGV[4])

redis.call('ZREMRANGEBYSCORE', key, '0', tostring(window_start))
local count = redis.call('ZCOUNT', key, tostring(window_start), '+inf')
redis.call('ZADD', key, now, member)
redis.call('EXPIRE', key, ttl)

return count
`)

// limitForPlan maps a plan name to its request ceiling. Unknown/empty plans
// and guests fall back to the anonymous ceiling.
func (c Config) limitForPlan(plan string) int {
	switch strings.ToLower(strings.TrimSpace(plan)) {
	case "pro":
		return c.ProLimit
	case "free":
		return c.FreeLimit
	default:
		return c.AnonLimit
	}
}

// shouldLimit reports whether a request path is subject to API rate limiting.
// Only /api/* routes are limited. /auth/* is already throttled by auth-service;
// /metrics, /healthz, the SPA, and the presigned MinIO bucket proxies are not
// rate limited here.
func shouldLimit(path string) bool {
	return strings.HasPrefix(path, "/api/") || path == "/api"
}

// identity derives the rate-limit bucket key suffix and the plan for a request.
// Authenticated users are keyed by user ID; everyone else by client IP. Guests
// are always treated as the anonymous plan regardless of any cached plan value.
func identity(authCtx authverify.AuthContext, ok bool, clientIP string) (subject, plan string) {
	if ok && !authCtx.IsGuest && strings.TrimSpace(authCtx.UserID) != "" {
		return "user:" + authCtx.UserID, authCtx.Plan
	}
	return "ip:" + clientIP, ""
}

// clientIP extracts the originating client IP, honoring X-Forwarded-For
// (leftmost entry) when present since the gateway runs behind a trusted proxy,
// and falling back to the connection's remote address.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first := strings.TrimSpace(strings.Split(xff, ",")[0]); first != "" {
			return first
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// Middleware returns an http middleware that enforces per-plan sliding-window
// limits on /api/* routes. It must run AFTER the auth middleware so the request
// context carries the resolved AuthContext (user ID + plan).
func Middleware(cfg Config) func(http.Handler) http.Handler {
	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = defaultKeyPrefix
	}
	if cfg.Window <= 0 {
		cfg.Window = time.Minute
	}
	if cfg.AnonLimit <= 0 {
		cfg.AnonLimit = 30
	}
	if cfg.FreeLimit <= 0 {
		cfg.FreeLimit = 120
	}
	if cfg.ProLimit <= 0 {
		cfg.ProLimit = 600
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Fail open: never block requests when limiting isn't applicable
			// or Redis is unavailable.
			if cfg.Client == nil || !shouldLimit(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			authCtx, ok := authverify.FromContext(r.Context())
			subject, plan := identity(authCtx, ok, clientIP(r))
			limit := cfg.limitForPlan(plan)
			key := fmt.Sprintf("%s:%s:%s", cfg.KeyPrefix, planLabel(plan), subject)

			count, err := cfg.allow(r.Context(), key)
			if err != nil {
				slog.Warn("api rate limit check failed, allowing request", "key", key, "error", err)
				next.ServeHTTP(w, r)
				return
			}

			remaining := limit - int(count) - 1
			if remaining < 0 {
				remaining = 0
			}
			resetUnix := time.Now().Add(cfg.Window).Unix()
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetUnix, 10))

			if count >= int64(limit) {
				retryAfter := int(cfg.Window.Seconds())
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				response.WriteErr(w, http.StatusTooManyRequests, "RATE_LIMIT_EXCEEDED",
					fmt.Sprintf("Too many requests. Please try again in %d seconds.", retryAfter))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// allow runs the sliding-window script and returns the pre-insert request count
// within the current window for key.
func (cfg Config) allow(ctx context.Context, key string) (int64, error) {
	now := time.Now()
	windowStart := now.Add(-cfg.Window)
	ttlSeconds := int(cfg.Window.Seconds()) * 2
	return slidingWindowLua.Run(ctx, cfg.Client, []string{key},
		windowStart.UnixNano(),
		now.UnixNano(),
		strconv.FormatInt(now.UnixNano(), 10),
		ttlSeconds,
	).Int64()
}

// planLabel normalizes a plan into the label used in Redis keys.
func planLabel(plan string) string {
	switch strings.ToLower(strings.TrimSpace(plan)) {
	case "pro":
		return "pro"
	case "free":
		return "free"
	default:
		return "anon"
	}
}

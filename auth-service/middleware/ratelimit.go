// Package middleware provides auth-service's per-endpoint, Redis-backed
// sliding-window rate limiter. It throttles sensitive public endpoints (login,
// signup, refresh, password reset, proxy login) by client IP and fails open so
// a Redis outage never blocks authentication.
package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"fyredocs/shared/response"
)

// RateLimitConfig configures a RateLimiter. Burst, when positive, is added to
// MaxRequests to form the effective ceiling within Window.
type RateLimitConfig struct {
	RedisClient *redis.Client
	KeyPrefix   string
	MaxRequests int
	Window      time.Duration
	Burst       int
}

// RateLimiter enforces a sliding-window request ceiling for a single endpoint,
// keyed by a caller-supplied prefix.
type RateLimiter struct {
	client      *redis.Client
	keyPrefix   string
	maxRequests int
	window      time.Duration
	burst       int
	script      *redis.Script
}

// rateLimitLua atomically trims the window, counts current entries, records this
// request, and refreshes the key TTL. Returns the pre-insert count.
var rateLimitLua = redis.NewScript(`
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

// NewRateLimiter builds a RateLimiter, applying sane defaults for an empty
// prefix, non-positive request ceiling, or non-positive window.
func NewRateLimiter(config RateLimitConfig) *RateLimiter {
	if config.KeyPrefix == "" {
		config.KeyPrefix = "ratelimit"
	}
	if config.MaxRequests <= 0 {
		config.MaxRequests = 10
	}
	if config.Window <= 0 {
		config.Window = time.Minute
	}
	return &RateLimiter{
		client:      config.RedisClient,
		keyPrefix:   config.KeyPrefix,
		maxRequests: config.MaxRequests,
		window:      config.Window,
		burst:       config.Burst,
		script:      rateLimitLua,
	}
}

// RateLimitByIP returns a Gin middleware that limits requests by client IP. It
// sets the standard X-RateLimit-* headers and aborts with 429 once the ceiling
// is exceeded. It fails open: a nil client or a Redis error lets the request
// through rather than locking users out.
func (rl *RateLimiter) RateLimitByIP() gin.HandlerFunc {
	return func(c *gin.Context) {
		if rl.client == nil {
			slog.Warn("rate limiter Redis unavailable, allowing request")
			c.Next()
			return
		}

		clientIP := c.ClientIP()

		allowed, remaining, resetTime, err := rl.checkLimit(c.Request.Context(), clientIP)
		if err != nil {
			slog.Error("rate limit check failed", "ip", clientIP, "error", err)
			c.Next()
			return
		}

		c.Header("X-RateLimit-Limit", strconv.Itoa(rl.maxRequests))
		c.Header("X-RateLimit-Remaining", strconv.Itoa(remaining))
		c.Header("X-RateLimit-Reset", strconv.FormatInt(resetTime.Unix(), 10))

		if !allowed {
			retryAfter := int(time.Until(resetTime).Seconds())
			if retryAfter < 0 {
				retryAfter = 0
			}
			c.Header("Retry-After", strconv.Itoa(retryAfter))
			response.AbortErr(c, http.StatusTooManyRequests, "RATE_LIMIT_EXCEEDED",
				fmt.Sprintf("Too many requests. Please try again in %d seconds.", retryAfter))
			return
		}

		c.Next()
	}
}

// checkLimit runs the sliding-window script for identifier and reports whether
// the request is allowed, how many requests remain in the window, and when the
// window resets.
func (rl *RateLimiter) checkLimit(ctx context.Context, identifier string) (allowed bool, remaining int, resetTime time.Time, err error) {
	key := fmt.Sprintf("%s:%s", rl.keyPrefix, identifier)
	now := time.Now()
	windowStart := now.Add(-rl.window)
	ttlSeconds := int(rl.window.Seconds()) * 2

	count, err := rl.script.Run(ctx, rl.client, []string{key},
		windowStart.UnixNano(),
		now.UnixNano(),
		fmt.Sprintf("%d", now.UnixNano()),
		ttlSeconds,
	).Int64()
	if err != nil {
		return false, 0, time.Time{}, err
	}

	maxAllowed := rl.maxRequests
	if rl.burst > 0 {
		maxAllowed = rl.maxRequests + rl.burst
	}

	remaining = maxAllowed - int(count) - 1
	if remaining < 0 {
		remaining = 0
	}

	resetTime = now.Add(rl.window)
	allowed = count < int64(maxAllowed)

	return allowed, remaining, resetTime, nil
}

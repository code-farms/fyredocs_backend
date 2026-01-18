package middleware

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

type RateLimitConfig struct {
	RedisClient *redis.Client
	KeyPrefix   string
	MaxRequests int
	Window      time.Duration
	Burst       int // Optional: allow burst above limit
}

type RateLimiter struct {
	client      *redis.Client
	keyPrefix   string
	maxRequests int
	window      time.Duration
	burst       int
}

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
	}
}

// RateLimitByIP creates middleware that rate limits by client IP
func (rl *RateLimiter) RateLimitByIP() gin.HandlerFunc {
	return func(c *gin.Context) {
		if rl.client == nil {
			// If Redis unavailable, fail open (allow request) but log warning
			log.Printf("WARNING: Rate limiter Redis unavailable, allowing request")
			c.Next()
			return
		}

		// Get client IP (respects X-Forwarded-For if behind proxy)
		clientIP := c.ClientIP()

		allowed, remaining, resetTime, err := rl.checkLimit(c.Request.Context(), clientIP)
		if err != nil {
			// On error, fail open but log
			log.Printf("ERROR: Rate limit check failed for %s: %v", clientIP, err)
			c.Next()
			return
		}

		// Set rate limit headers (helpful for clients)
		c.Header("X-RateLimit-Limit", strconv.Itoa(rl.maxRequests))
		c.Header("X-RateLimit-Remaining", strconv.Itoa(remaining))
		c.Header("X-RateLimit-Reset", strconv.FormatInt(resetTime.Unix(), 10))

		if !allowed {
			retryAfter := int(time.Until(resetTime).Seconds())
			if retryAfter < 0 {
				retryAfter = 0
			}
			c.Header("Retry-After", strconv.Itoa(retryAfter))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"code":    "RATE_LIMIT_EXCEEDED",
				"message": fmt.Sprintf("Too many requests. Please try again in %d seconds.", retryAfter),
			})
			return
		}

		c.Next()
	}
}

// checkLimit implements sliding window algorithm using Redis sorted sets
func (rl *RateLimiter) checkLimit(ctx context.Context, identifier string) (allowed bool, remaining int, resetTime time.Time, err error) {
	key := fmt.Sprintf("%s:%s", rl.keyPrefix, identifier)
	now := time.Now()
	windowStart := now.Add(-rl.window)

	pipe := rl.client.Pipeline()

	// Remove old entries outside the window
	pipe.ZRemRangeByScore(ctx, key, "0", strconv.FormatInt(windowStart.UnixNano(), 10))

	// Count current requests in window
	zCount := pipe.ZCount(ctx, key, strconv.FormatInt(windowStart.UnixNano(), 10), "+inf")

	// Add current request with timestamp as score
	pipe.ZAdd(ctx, key, redis.Z{
		Score:  float64(now.UnixNano()),
		Member: fmt.Sprintf("%d", now.UnixNano()),
	})

	// Set expiration on key (cleanup)
	pipe.Expire(ctx, key, rl.window*2)

	_, err = pipe.Exec(ctx)
	if err != nil {
		return false, 0, time.Time{}, err
	}

	count, err := zCount.Result()
	if err != nil {
		return false, 0, time.Time{}, err
	}

	// Allow burst if configured
	maxAllowed := rl.maxRequests
	if rl.burst > 0 {
		maxAllowed = rl.maxRequests + rl.burst
	}

	remaining = maxAllowed - int(count) - 1 // -1 for current request
	if remaining < 0 {
		remaining = 0
	}

	resetTime = now.Add(rl.window)
	allowed = count < int64(maxAllowed)

	return allowed, remaining, resetTime, nil
}

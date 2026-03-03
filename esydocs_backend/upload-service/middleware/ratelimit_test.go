package middleware

import (
	"testing"
	"time"
)

func TestNewRateLimiterDefaults(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{})
	if rl.keyPrefix != "ratelimit" {
		t.Errorf("expected default keyPrefix 'ratelimit', got %q", rl.keyPrefix)
	}
	if rl.maxRequests != 10 {
		t.Errorf("expected default maxRequests 10, got %d", rl.maxRequests)
	}
	if rl.window != time.Minute {
		t.Errorf("expected default window 1m, got %v", rl.window)
	}
}

func TestNewRateLimiterCustom(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{
		KeyPrefix:   "custom",
		MaxRequests: 100,
		Window:      5 * time.Minute,
		Burst:       20,
	})
	if rl.keyPrefix != "custom" {
		t.Errorf("expected keyPrefix 'custom', got %q", rl.keyPrefix)
	}
	if rl.maxRequests != 100 {
		t.Errorf("expected maxRequests 100, got %d", rl.maxRequests)
	}
	if rl.window != 5*time.Minute {
		t.Errorf("expected window 5m, got %v", rl.window)
	}
	if rl.burst != 20 {
		t.Errorf("expected burst 20, got %d", rl.burst)
	}
}

func TestRateLimitByIPNilClient(t *testing.T) {
	// With nil Redis client, rate limiter should allow all requests (fail open)
	rl := NewRateLimiter(RateLimitConfig{
		RedisClient: nil,
	})
	handler := rl.RateLimitByIP()
	if handler == nil {
		t.Error("RateLimitByIP should return non-nil handler even with nil client")
	}
}

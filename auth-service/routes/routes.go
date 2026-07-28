// Package routes maps auth-service HTTP paths to handlers and owns the
// per-endpoint rate-limit configuration.
//
// The route groups encode the trust model: /auth public endpoints take no
// token, the protected /auth group requires a valid one, and /internal is
// service-to-service only, guarded by a shared secret that fails closed. Each
// sensitive endpoint gets its own IP limiter with its own ceiling — login,
// signup, refresh, password reset, and admin impersonation are limited
// independently so exhausting one cannot lock a user out of the others.
package routes

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"auth-service/handlers"
	"auth-service/internal/email"
	"auth-service/internal/models"
	"auth-service/internal/token"
	"auth-service/middleware"
	"fyredocs/shared/authverify"

	"fyredocs/shared/config"
)

// SetupRouter wires the auth-service routes onto r. It registers per-endpoint
// IP rate limiters, then mounts the public auth routes, the token-protected auth
// routes, the internal service-to-service API, and the health/readiness probes.
func SetupRouter(r *gin.Engine, issuer *token.Issuer, denylist authverify.TokenDenylist, redisClient *redis.Client, mailer email.Mailer, authMiddleware gin.HandlerFunc) {
	authEndpoints := &handlers.AuthEndpoints{
		Issuer:      issuer,
		Denylist:    denylist,
		RedisClient: redisClient,
		Mailer:      mailer,
	}

	window := config.GetEnvDuration("RATE_LIMIT_WINDOW", 60*time.Second)

	loginLimiter := middleware.NewRateLimiter(middleware.RateLimitConfig{
		RedisClient: redisClient,
		KeyPrefix:   "ratelimit:login",
		MaxRequests: config.GetEnvInt("RATE_LIMIT_LOGIN", 5),
		Window:      window,
	})

	signupLimiter := middleware.NewRateLimiter(middleware.RateLimitConfig{
		RedisClient: redisClient,
		KeyPrefix:   "ratelimit:signup",
		MaxRequests: config.GetEnvInt("RATE_LIMIT_SIGNUP", 3),
		Window:      window,
	})

	refreshLimiter := middleware.NewRateLimiter(middleware.RateLimitConfig{
		RedisClient: redisClient,
		KeyPrefix:   "ratelimit:refresh",
		MaxRequests: config.GetEnvInt("RATE_LIMIT_REFRESH", 10),
		Window:      window,
	})

	forgotPasswordLimiter := middleware.NewRateLimiter(middleware.RateLimitConfig{
		RedisClient: redisClient,
		KeyPrefix:   "ratelimit:forgot_password",
		MaxRequests: config.GetEnvInt("RATE_LIMIT_FORGOT_PASSWORD", 3),
		Window:      window,
	})

	resetPasswordLimiter := middleware.NewRateLimiter(middleware.RateLimitConfig{
		RedisClient: redisClient,
		KeyPrefix:   "ratelimit:reset_password",
		MaxRequests: config.GetEnvInt("RATE_LIMIT_RESET_PASSWORD", 5),
		Window:      window,
	})

	proxyLoginLimiter := middleware.NewRateLimiter(middleware.RateLimitConfig{
		RedisClient: redisClient,
		KeyPrefix:   "ratelimit:proxy_login",
		MaxRequests: config.GetEnvInt("RATE_LIMIT_PROXY_LOGIN", 5),
		Window:      window,
	})

	// Public auth routes — no token verification needed
	publicAuth := r.Group("/auth")
	{
		publicAuth.POST("/signup", signupLimiter.RateLimitByIP(), authEndpoints.Signup)
		publicAuth.POST("/login", loginLimiter.RateLimitByIP(), authEndpoints.Login)
		publicAuth.POST("/refresh", refreshLimiter.RateLimitByIP(), authEndpoints.Refresh)
		publicAuth.POST("/forgot-password", forgotPasswordLimiter.RateLimitByIP(), authEndpoints.ForgotPassword)
		publicAuth.POST("/reset-password", resetPasswordLimiter.RateLimitByIP(), authEndpoints.ResetPassword)
		publicAuth.GET("/plans", handlers.GetAllPlans)
	}

	// Protected auth routes — require valid token
	protectedAuth := r.Group("/auth", authMiddleware)
	{
		protectedAuth.GET("/me", authEndpoints.Me)
		protectedAuth.GET("/profile", authEndpoints.Profile)
		protectedAuth.POST("/logout", authEndpoints.Logout)
		protectedAuth.PUT("/plan", authEndpoints.ChangePlan)
		protectedAuth.POST("/proxy-login", proxyLoginLimiter.RateLimitByIP(), authEndpoints.ProxyLogin)
	}

	// Internal service-to-service API (not exposed via gateway). Guarded by the
	// INTERNAL_API_TOKEN shared secret; the middleware fails closed (503) when
	// the token is unset so network isolation is never the only defense.
	internal := r.Group("/internal", authverify.InternalTokenGin(config.GetEnv("INTERNAL_API_TOKEN", "")))
	{
		internal.GET("/users/:id/plan", handlers.GetUserPlan)
		internal.POST("/users/:id/revoke-sessions", authEndpoints.RevokeUserSessions)
		internal.DELETE("/sessions/:id", authEndpoints.RevokeSession)
	}

	r.GET("/healthz", func(c *gin.Context) {
		c.String(200, "ok")
	})

	// Readiness probe: reports 503 unless Postgres and Redis are both reachable.
	r.GET("/readyz", func(c *gin.Context) {
		checks := gin.H{}
		ready := true

		hctx, hcancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer hcancel()

		if err := models.DB.Exec("SELECT 1").Error; err != nil {
			// Raw driver errors can leak internal host:port details; the probe
			// body stays generic and the cause goes to logs.
			slog.Warn("readyz dependency check failed", "dependency", "postgres", "error", err)
			checks["postgres"] = "unavailable"
			ready = false
		} else {
			checks["postgres"] = "ok"
		}

		if err := redisClient.Ping(hctx).Err(); err != nil {
			// Raw driver errors can leak internal host:port details; the probe
			// body stays generic and the cause goes to logs.
			slog.Warn("readyz dependency check failed", "dependency", "redis", "error", err)
			checks["redis"] = "unavailable"
			ready = false
		} else {
			checks["redis"] = "ok"
		}

		if !ready {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready", "checks": checks})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready", "checks": checks})
	})
}

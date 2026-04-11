package routes

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"auth-service/handlers"
	"auth-service/internal/authverify"
	"auth-service/internal/models"
	"auth-service/internal/token"
	"auth-service/middleware"

	"esydocs/shared/config"
)

func SetupRouter(r *gin.Engine, issuer *token.Issuer, denylist authverify.TokenDenylist, redisClient *redis.Client, authMiddleware gin.HandlerFunc) {
	authEndpoints := &handlers.AuthEndpoints{
		Issuer:      issuer,
		Denylist:    denylist,
		RedisClient: redisClient,
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

	// Public auth routes — no token verification needed
	publicAuth := r.Group("/auth")
	{
		publicAuth.POST("/signup", signupLimiter.RateLimitByIP(), authEndpoints.Signup)
		publicAuth.POST("/login", loginLimiter.RateLimitByIP(), authEndpoints.Login)
		publicAuth.POST("/refresh", refreshLimiter.RateLimitByIP(), authEndpoints.Refresh)
		publicAuth.GET("/plans", handlers.GetAllPlans)
	}

	// Protected auth routes — require valid token
	protectedAuth := r.Group("/auth", authMiddleware)
	{
		protectedAuth.GET("/me", authEndpoints.Me)
		protectedAuth.GET("/profile", authEndpoints.Profile)
		protectedAuth.POST("/logout", authEndpoints.Logout)
		protectedAuth.PUT("/plan", authEndpoints.ChangePlan)
	}

	// Internal service-to-service API (not exposed via gateway)
	internal := r.Group("/internal")
	{
		internal.GET("/users/:id/plan", handlers.GetUserPlan)
		internal.POST("/users/:id/revoke-sessions", authEndpoints.RevokeUserSessions)
		internal.DELETE("/sessions/:id", authEndpoints.RevokeSession)
	}

	r.GET("/healthz", func(c *gin.Context) {
		c.String(200, "ok")
	})

	r.GET("/readyz", func(c *gin.Context) {
		checks := gin.H{}
		ready := true

		hctx, hcancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer hcancel()

		// Check PostgreSQL
		if err := models.DB.Exec("SELECT 1").Error; err != nil {
			checks["postgres"] = err.Error()
			ready = false
		} else {
			checks["postgres"] = "ok"
		}

		// Check Redis
		if err := redisClient.Ping(hctx).Err(); err != nil {
			checks["redis"] = err.Error()
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


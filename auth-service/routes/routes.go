package routes

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"auth-service/handlers"
	"auth-service/internal/authverify"
	"auth-service/internal/models"
	"auth-service/internal/token"
	"auth-service/middleware"
)

func SetupRouter(r *gin.Engine, issuer *token.Issuer, denylist authverify.TokenDenylist, redisClient *redis.Client) {
	authEndpoints := &handlers.AuthEndpoints{
		Issuer:   issuer,
		Denylist: denylist,
	}

	window := getEnvDuration("RATE_LIMIT_WINDOW", 60*time.Second)

	loginLimiter := middleware.NewRateLimiter(middleware.RateLimitConfig{
		RedisClient: redisClient,
		KeyPrefix:   "ratelimit:login",
		MaxRequests: getEnvInt("RATE_LIMIT_LOGIN", 5),
		Window:      window,
	})

	signupLimiter := middleware.NewRateLimiter(middleware.RateLimitConfig{
		RedisClient: redisClient,
		KeyPrefix:   "ratelimit:signup",
		MaxRequests: getEnvInt("RATE_LIMIT_SIGNUP", 3),
		Window:      window,
	})

	refreshLimiter := middleware.NewRateLimiter(middleware.RateLimitConfig{
		RedisClient: redisClient,
		KeyPrefix:   "ratelimit:refresh",
		MaxRequests: getEnvInt("RATE_LIMIT_REFRESH", 10),
		Window:      window,
	})

	authGroup := r.Group("/auth")
	{
		authGroup.POST("/signup", signupLimiter.RateLimitByIP(), authEndpoints.Signup)
		authGroup.POST("/login", loginLimiter.RateLimitByIP(), authEndpoints.Login)
		authGroup.POST("/refresh", refreshLimiter.RateLimitByIP(), authEndpoints.Refresh)
		authGroup.GET("/me", authEndpoints.Me)
		authGroup.GET("/profile", authEndpoints.Profile)
		authGroup.POST("/logout", authEndpoints.Logout)
		authGroup.PUT("/plan", authEndpoints.ChangePlan)
		authGroup.GET("/plans", handlers.GetAllPlans)
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

func getEnvInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

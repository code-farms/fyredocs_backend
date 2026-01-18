package routes

import (
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"upload-service/auth"
	"upload-service/handlers"
	"upload-service/middleware"
	"upload-service/redisstore"
)

func SetupUploadRouter(r *gin.Engine) {
	api := r.Group("/api")
	{
		uploads := api.Group("/uploads")
		uploads.POST("/init", handlers.InitUpload)
		uploads.PUT("/:uploadId/chunk", handlers.UploadChunk)
		uploads.GET("/:uploadId/status", handlers.GetUploadStatus)
		uploads.POST("/:uploadId/complete", handlers.CompleteUpload)

		convertFrom := api.Group("/convert-from-pdf", auth.RequireAuthenticatedGin())
		convertFrom.GET("/:tool", handlers.GetJobsByTool)
		convertFrom.POST("/:tool", handlers.CreateJobFromTool)
		convertFrom.GET("/:tool/:id", handlers.GetJobByID)
		convertFrom.DELETE("/:tool/:id", handlers.DeleteJobByID)
		convertFrom.GET("/:tool/:id/download", handlers.DownloadJobFile)

		convertTo := api.Group("/convert-to-pdf", auth.RequireAuthenticatedGin())
		convertTo.GET("/:tool", handlers.GetJobsByTool)
		convertTo.POST("/:tool", handlers.CreateJobFromTool)
		convertTo.GET("/:tool/:id", handlers.GetJobByID)
		convertTo.DELETE("/:tool/:id", handlers.DeleteJobByID)
		convertTo.GET("/:tool/:id/download", handlers.DownloadJobFile)

		api.GET("/jobs/history", auth.RequireAuthenticatedGin(), handlers.GetJobHistory)
	}

	// Create rate limiters for auth endpoints
	window := getEnvDuration("RATE_LIMIT_WINDOW", 60*time.Second)

	loginLimiter := middleware.NewRateLimiter(middleware.RateLimitConfig{
		RedisClient: redisstore.Client,
		KeyPrefix:   "ratelimit:login",
		MaxRequests: getEnvInt("RATE_LIMIT_LOGIN", 5),
		Window:      window,
	})

	signupLimiter := middleware.NewRateLimiter(middleware.RateLimitConfig{
		RedisClient: redisstore.Client,
		KeyPrefix:   "ratelimit:signup",
		MaxRequests: getEnvInt("RATE_LIMIT_SIGNUP", 3),
		Window:      window,
	})

	refreshLimiter := middleware.NewRateLimiter(middleware.RateLimitConfig{
		RedisClient: redisstore.Client,
		KeyPrefix:   "ratelimit:refresh",
		MaxRequests: getEnvInt("RATE_LIMIT_REFRESH", 10),
		Window:      window,
	})

	authGroup := r.Group("/auth")
	{
		authGroup.POST("/signup", signupLimiter.RateLimitByIP(), handlers.Signup)
		authGroup.POST("/login", loginLimiter.RateLimitByIP(), handlers.Login)
		authGroup.POST("/refresh", refreshLimiter.RateLimitByIP(), handlers.Refresh)
		authGroup.GET("/me", handlers.Me)
		authGroup.GET("/profile", handlers.Profile)
		authGroup.POST("/logout", handlers.Logout)
	}

	r.GET("/healthz", func(c *gin.Context) {
		c.String(200, "ok")
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

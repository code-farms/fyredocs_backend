package routes

import (
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"esydocs/shared/redisstore"

	"job-service/handlers"
	"job-service/internal/authverify"
	"job-service/middleware"
)

func SetupRouter(r *gin.Engine) {
	window := getEnvDuration("RATE_LIMIT_WINDOW", 60*time.Second)

	uploadLimiter := middleware.NewRateLimiter(middleware.RateLimitConfig{
		RedisClient: redisstore.Client,
		KeyPrefix:   "ratelimit:upload",
		MaxRequests: getEnvInt("RATE_LIMIT_UPLOAD", 30),
		Window:      window,
	})

	api := r.Group("/api")
	{
		uploads := api.Group("/uploads", uploadLimiter.RateLimitByIP())
		uploads.POST("/init", handlers.InitUpload)
		uploads.PUT("/:uploadId/chunk", handlers.UploadChunk)
		uploads.GET("/:uploadId/status", handlers.GetUploadStatus)
		uploads.POST("/:uploadId/complete", handlers.CompleteUpload)

		convertFrom := api.Group("/convert-from-pdf", authverify.RequireAuthenticatedGin())
		convertFrom.GET("/:tool", handlers.GetJobsByTool)
		convertFrom.POST("/:tool", handlers.CreateJobFromTool)
		convertFrom.GET("/:tool/:id", handlers.GetJobByID)
		convertFrom.DELETE("/:tool/:id", handlers.DeleteJobByID)
		convertFrom.GET("/:tool/:id/download", handlers.DownloadJobFile)

		convertTo := api.Group("/convert-to-pdf", authverify.RequireAuthenticatedGin())
		convertTo.GET("/:tool", handlers.GetJobsByTool)
		convertTo.POST("/:tool", handlers.CreateJobFromTool)
		convertTo.GET("/:tool/:id", handlers.GetJobByID)
		convertTo.DELETE("/:tool/:id", handlers.DeleteJobByID)
		convertTo.GET("/:tool/:id/download", handlers.DownloadJobFile)

		api.GET("/jobs/history", authverify.RequireAuthenticatedGin(), handlers.GetJobHistory)
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

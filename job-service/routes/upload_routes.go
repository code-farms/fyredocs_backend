package routes

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"fyredocs/shared/config"
	"fyredocs/shared/natsconn"
	"fyredocs/shared/redisstore"

	"job-service/handlers"
	"job-service/internal/authverify"
	"job-service/internal/models"
	"job-service/middleware"
)

func SetupRouter(r *gin.Engine) {
	window := config.GetEnvDuration("RATE_LIMIT_WINDOW", 60*time.Second)

	uploadLimiter := middleware.NewRateLimiter(middleware.RateLimitConfig{
		RedisClient: redisstore.Client,
		KeyPrefix:   "ratelimit:upload",
		MaxRequests: config.GetEnvInt("RATE_LIMIT_UPLOAD", 30),
		Window:      window,
	})

	// Job creation publishes to NATS and writes to Postgres — rate-limit it
	// separately from the (cheap) presign-only upload endpoints.
	jobCreateLimiter := middleware.NewRateLimiter(middleware.RateLimitConfig{
		RedisClient: redisstore.Client,
		KeyPrefix:   "ratelimit:jobcreate",
		MaxRequests: config.GetEnvInt("RATE_LIMIT_JOB_CREATE", 20),
		Window:      window,
	})

	api := r.Group("/api")
	{
		// Presigned multipart upload protocol: the browser gets presigned part
		// URLs from /init (or re-presigned from /parts), PUTs file bytes
		// directly to MinIO/S3, then calls /complete with the part ETags.
		uploads := api.Group("/uploads", uploadLimiter.RateLimitByIP())
		uploads.POST("/init", handlers.InitUpload)
		uploads.GET("/:uploadId/parts", handlers.GetUploadParts)
		uploads.POST("/:uploadId/complete", handlers.CompleteUpload)
		uploads.GET("/:uploadId/status", handlers.GetUploadStatus)
		uploads.DELETE("/:uploadId", handlers.AbortUpload)
		// One-release migration stub for the retired chunk-streaming protocol:
		// always answers 410 UPLOAD_PROTOCOL_CHANGED. Remove this route (and
		// handlers.UploadChunk) in the release after the frontend ships the
		// presigned protocol.
		uploads.PUT("/:uploadId/chunk", handlers.UploadChunk)

		convertFrom := api.Group("/convert-from-pdf")
		convertFrom.GET("/:tool", handlers.GetJobsByTool)
		convertFrom.POST("/:tool", jobCreateLimiter.RateLimitByIP(), handlers.CreateJobFromTool)
		convertFrom.GET("/:tool/:id", handlers.GetJobByID)
		convertFrom.DELETE("/:tool/:id", handlers.DeleteJobByID)
		convertFrom.GET("/:tool/:id/download", handlers.DownloadJobFile)

		convertTo := api.Group("/convert-to-pdf")
		convertTo.GET("/:tool", handlers.GetJobsByTool)
		convertTo.POST("/:tool", jobCreateLimiter.RateLimitByIP(), handlers.CreateJobFromTool)
		convertTo.GET("/:tool/:id", handlers.GetJobByID)
		convertTo.DELETE("/:tool/:id", handlers.DeleteJobByID)
		convertTo.GET("/:tool/:id/download", handlers.DownloadJobFile)

		organizePdf := api.Group("/organize-pdf")
		organizePdf.GET("/:tool", handlers.GetJobsByTool)
		organizePdf.POST("/:tool", jobCreateLimiter.RateLimitByIP(), handlers.CreateJobFromTool)
		organizePdf.GET("/:tool/:id", handlers.GetJobByID)
		organizePdf.DELETE("/:tool/:id", handlers.DeleteJobByID)
		organizePdf.GET("/:tool/:id/download", handlers.DownloadJobFile)

		optimizePdf := api.Group("/optimize-pdf")
		optimizePdf.GET("/:tool", handlers.GetJobsByTool)
		optimizePdf.POST("/:tool", jobCreateLimiter.RateLimitByIP(), handlers.CreateJobFromTool)
		optimizePdf.GET("/:tool/:id", handlers.GetJobByID)
		optimizePdf.DELETE("/:tool/:id", handlers.DeleteJobByID)
		optimizePdf.GET("/:tool/:id/download", handlers.DownloadJobFile)

		api.GET("/jobs/history", authverify.RequireAuthenticatedGin(), handlers.GetJobHistory)
		api.GET("/jobs/:id/events", handlers.SSEJobUpdates)
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
		if err := redisstore.Client.Ping(hctx).Err(); err != nil {
			checks["redis"] = err.Error()
			ready = false
		} else {
			checks["redis"] = "ok"
		}

		// Check NATS
		if natsconn.Conn == nil || !natsconn.Conn.IsConnected() {
			checks["nats"] = "disconnected"
			ready = false
		} else {
			checks["nats"] = "ok"
		}

		if !ready {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready", "checks": checks})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready", "checks": checks})
	})
}


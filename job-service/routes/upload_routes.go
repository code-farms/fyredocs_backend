// Package routes maps job-service HTTP paths to handlers.
//
// Rate limiters are split by cost rather than applied uniformly: presign-only
// upload calls are cheap, job creation writes to Postgres and publishes to
// NATS, and scanner edge detection is a synchronous relay to organize-pdf.
// Giving them one shared bucket would let cheap polling starve job creation.
// The four per-tool-group route blocks are intentionally identical in shape —
// the tool name is a path parameter resolved through internal/routing.
package routes

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"fyredocs/shared/config"
	"fyredocs/shared/natsconn"
	"fyredocs/shared/redisstore"

	"fyredocs/shared/authverify"
	"job-service/handlers"
	"job-service/internal/models"
	"job-service/middleware"
)

// SetupRouter wires the job-service routes onto r: the presigned upload
// protocol, per-tool job creation/query/download endpoints, job history and SSE
// updates, and the health/readiness probes. Sensitive endpoints get their own
// IP rate limiters keyed by concern (upload, job creation, edge detection).
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

		// Edge detection for the mobile scanner: synchronous, cheap relay to
		// organize-pdf — rate-limited separately from job creation.
		detectLimiter := middleware.NewRateLimiter(middleware.RateLimitConfig{
			RedisClient: redisstore.Client,
			KeyPrefix:   "ratelimit:detect",
			MaxRequests: config.GetEnvInt("RATE_LIMIT_DETECT", 60),
			Window:      window,
		})

		organizePdf := api.Group("/organize-pdf")
		organizePdf.POST("/detect-edges", detectLimiter.RateLimitByIP(), handlers.DetectEdges)
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

		// Share links: create consumes an upload (authenticated or guest);
		// revoke is owner-only. The PUBLIC redeem lives outside /api below.
		shares := api.Group("/share")
		shares.POST("", jobCreateLimiter.RateLimitByIP(), handlers.CreateShare)
		shares.DELETE("/:id", handlers.RevokeShare)

		// Document backups (Phase 9): copy an upload into the caller's
		// durable backups/ prefix. Account-backed — auth enforced in-handler
		// too, but the middleware gives the clean 401.
		api.POST("/backup", authverify.RequireAuthenticatedGin(), jobCreateLimiter.RateLimitByIP(), handlers.CreateBackup)

		api.GET("/jobs/history", authverify.RequireAuthenticatedGin(), handlers.GetJobHistory)
		// Super-admin only (enforced in the handler via X-User-Role): re-dispatch
		// dead-lettered worker jobs from JOBS_DLQ.
		api.POST("/jobs/dlq/redrive", authverify.RequireAuthenticatedGin(), handlers.RedriveDLQ)
		api.GET("/jobs/:id/events", handlers.SSEJobUpdates)
	}

	// PUBLIC share-link redeem — no auth by design (the token IS the
	// credential: 256-bit, stored hashed). Unauthenticated and enumerable in
	// principle, so it gets its own tight rate limit; the gateway must list
	// /share in its public paths.
	shareRedeemLimiter := middleware.NewRateLimiter(middleware.RateLimitConfig{
		RedisClient: redisstore.Client,
		KeyPrefix:   "ratelimit:shareredeem",
		MaxRequests: config.GetEnvInt("RATE_LIMIT_SHARE_REDEEM", 30),
		Window:      window,
	})
	r.GET("/share/:token", shareRedeemLimiter.RateLimitByIP(), handlers.RedeemShare)

	r.GET("/healthz", func(c *gin.Context) {
		c.String(200, "ok")
	})

	// Readiness probe: reports 503 unless Postgres, Redis, and NATS are all reachable.
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

		if err := redisstore.Client.Ping(hctx).Err(); err != nil {
			// Raw driver errors can leak internal host:port details; the probe
			// body stays generic and the cause goes to logs.
			slog.Warn("readyz dependency check failed", "dependency", "redis", "error", err)
			checks["redis"] = "unavailable"
			ready = false
		} else {
			checks["redis"] = "ok"
		}

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

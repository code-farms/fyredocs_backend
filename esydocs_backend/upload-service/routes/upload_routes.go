package routes

import (
	"github.com/gin-gonic/gin"

	"upload-service/auth"
	"upload-service/handlers"
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

	r.GET("/healthz", func(c *gin.Context) {
		c.String(200, "ok")
	})
}

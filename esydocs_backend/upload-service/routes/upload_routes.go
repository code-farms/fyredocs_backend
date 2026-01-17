package routes

import (
	"upload-service/handlers"
	"github.com/gin-gonic/gin"
)

func SetupUploadRouter(r *gin.Engine) {
	api := r.Group("/api")
	{
		api.POST("/uploads/init", handlers.InitUpload)
		api.PUT("/uploads/:uploadId/chunk", handlers.UploadChunk)
		api.GET("/uploads/:uploadId/status", handlers.GetUploadStatus)
		api.POST("/uploads/:uploadId/complete", handlers.CompleteUpload)

		api.GET("/convert-from-pdf/:tool", handlers.GetJobsByTool)
		api.POST("/convert-from-pdf/:tool", handlers.CreateJobFromTool)
		api.GET("/convert-from-pdf/:tool/:id", handlers.GetJobByID)
		api.DELETE("/convert-from-pdf/:tool/:id", handlers.DeleteJobByID)
		api.GET("/convert-from-pdf/:tool/:id/download", handlers.DownloadJobFile)

		api.GET("/convert-to-pdf/:tool", handlers.GetJobsByTool)
		api.POST("/convert-to-pdf/:tool", handlers.CreateJobFromTool)
		api.GET("/convert-to-pdf/:tool/:id", handlers.GetJobByID)
		api.DELETE("/convert-to-pdf/:tool/:id", handlers.DeleteJobByID)
		api.GET("/convert-to-pdf/:tool/:id/download", handlers.DownloadJobFile)

		api.GET("/jobs/history", handlers.GetJobHistory)
	}

	r.GET("/healthz", func(c *gin.Context) {
		c.String(200, "ok")
	})
}

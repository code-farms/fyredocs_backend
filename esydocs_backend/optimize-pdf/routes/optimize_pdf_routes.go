package routes

import (
	"github.com/gin-gonic/gin"

	"optimize-pdf/handlers"
)

func SetupOptimizePdfRouter(r *gin.Engine) {
	api := r.Group("/api/optimize-pdf")
	{
		api.GET("/:tool", handlers.GetJobs)
		api.POST("/:tool", handlers.CreateOptimizePdfJob)
		api.GET("/:tool/:id", handlers.GetJob)
		api.PATCH("/:tool/:id", handlers.UpdateJob)
		api.DELETE("/:tool/:id", handlers.DeleteJob)
		api.GET("/:tool/:id/download", handlers.DownloadFile)
	}
}

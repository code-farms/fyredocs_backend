package routes

import (
	"convert-to-pdf/handlers"
	"github.com/gin-gonic/gin"
)

func SetupConvertToPdfRouter(r *gin.Engine) {
	api := r.Group("/api/convert-to-pdf")
	{
		api.GET("/:tool", handlers.GetJobs)
		api.POST("/:tool", handlers.CreatePdfToJob)
		api.GET("/:tool/:id", handlers.GetJob)
		api.PATCH("/:tool/:id", handlers.UpdateJob)
		api.DELETE("/:tool/:id", handlers.DeleteJob)
		api.GET("/:tool/:id/download", handlers.DownloadFile)
	}
}

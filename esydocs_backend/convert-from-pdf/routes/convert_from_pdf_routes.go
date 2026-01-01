package routes

import (
	"convert-from-pdf/handlers"
	"github.com/gin-gonic/gin"
)

func SetupConvertFromPdfRouter(r *gin.Engine) {
	api := r.Group("/api/convert-from-pdf")
	{
		api.GET("/:tool", handlers.GetJobs)
		api.POST("/:tool", handlers.CreatePdfFromJob)
		api.GET("/:tool/:id", handlers.GetJob)
		api.PATCH("/:tool/:id", handlers.UpdateJob)
		api.DELETE("/:tool/:id", handlers.DeleteJob)
		api.GET("/:tool/:id/download", handlers.DownloadFile)
	}
}

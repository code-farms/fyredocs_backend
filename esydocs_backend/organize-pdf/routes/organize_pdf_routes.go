package routes

import (
	"organize-pdf/handlers"
	"github.com/gin-gonic/gin"
)

func SetupOrganizePdfRouter(r *gin.Engine) {
	api := r.Group("/api/organize-pdf")
	{
		api.GET("/:tool", handlers.GetJobs)
		api.POST("/:tool", handlers.CreateOrganizePdfJob)
		api.GET("/:tool/:id", handlers.GetJob)
		api.PATCH("/:tool/:id", handlers.UpdateJob)
		api.DELETE("/:tool/:id", handlers.DeleteJob)
		api.GET("/:tool/:id/download", handlers.DownloadFile)
	}
}

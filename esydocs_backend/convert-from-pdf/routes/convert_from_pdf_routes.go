package routes

import (
	"convert-from-pdf/handlers"
	"github.com/gin-gonic/gin"
)

func SetupConvertFromPdfRouter(r *gin.Engine) {
	api := r.Group("/api")
	{
		jobs := api.Group("/jobs")
		{
			jobs.GET("/", handlers.GetJobs)
			jobs.GET("/:id", handlers.GetJob)
			jobs.POST("/", handlers.CreatePdfFromJob)
			jobs.PATCH("/:id", handlers.UpdateJob)
			jobs.DELETE("/:id", handlers.DeleteJob)
		}
		api.GET("/download/:id", handlers.DownloadFile)
	}
}

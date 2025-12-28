package routes

import (
	"convert-to-pdf/handlers"
	"github.com/gin-gonic/gin"
)

func SetupConvertToPdfRouter(r *gin.Engine) {
	api := r.Group("/api")
	{
		jobs := api.Group("/jobs")
		{
			jobs.GET("/", handlers.GetJobs)
			jobs.GET("/:id", handlers.GetJob)
			jobs.POST("/", handlers.CreatePdfToJob)
			jobs.PATCH("/:id", handlers.UpdateJob)
			jobs.DELETE("/:id", handlers.DeleteJob)
		}
		api.GET("/download/:id", handlers.DownloadFile)
	}
}

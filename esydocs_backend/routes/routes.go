package routes

import (
	"esydocs_backend_go/handlers"
	"github.com/gin-gonic/gin"
)

func SetupRouter(r *gin.Engine) {
	api := r.Group("/api")
	{
		jobs := api.Group("/jobs")
		{
			jobs.GET("/", handlers.GetJobs)
			jobs.GET("/:id", handlers.GetJob)
			jobs.POST("/", handlers.CreateJob)
			jobs.PATCH("/:id", handlers.UpdateJob)
			jobs.DELETE("/:id", handlers.DeleteJob)
		}
		api.GET("/download/:id", handlers.DownloadFile)
	}
}

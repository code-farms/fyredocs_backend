package routes

import (
	"upload-service/handlers"
	"github.com/gin-gonic/gin"
)

func SetupUploadRouter(r *gin.Engine) {
	api := r.Group("/api")
	{
		api.POST("/uploads", handlers.UploadFiles)
	}
}

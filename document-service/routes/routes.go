package routes

import (
	"github.com/gin-gonic/gin"

	"document-service/handlers"
)

// SetupRouter registers health and the user-scoped document/folder/tag APIs.
// Identity comes from the gateway-injected X-User-ID header (RequireUser).
func SetupRouter(r *gin.Engine) {
	r.GET("/healthz", handlers.HealthCheck)
	r.GET("/readyz", handlers.ReadyCheck)

	api := r.Group("/api")
	api.Use(handlers.RequireUser())
	{
		docs := api.Group("/documents")
		{
			docs.GET("", handlers.ListDocuments)
			docs.POST("", handlers.CreateDocument)
			docs.GET("/:id", handlers.GetDocument)
			docs.PATCH("/:id", handlers.UpdateDocument)
			docs.DELETE("/:id", handlers.DeleteDocument)
			docs.POST("/:id/restore", handlers.RestoreDocument)
			docs.DELETE("/:id/permanent", handlers.PurgeDocument)
			docs.POST("/:id/tags", handlers.AttachTag)
			docs.DELETE("/:id/tags/:tagId", handlers.DetachTag)
			docs.POST("/workspace-hint", handlers.SetJobWorkspaceHint)
		}

		folders := api.Group("/folders")
		{
			folders.GET("", handlers.ListFolders)
			folders.POST("", handlers.CreateFolder)
			folders.PATCH("/:id", handlers.UpdateFolder)
			folders.DELETE("/:id", handlers.DeleteFolder)
		}

		tags := api.Group("/tags")
		{
			tags.GET("", handlers.ListTags)
			tags.POST("", handlers.CreateTag)
			tags.DELETE("/:id", handlers.DeleteTag)
		}

		exports := api.Group("/exports")
		{
			exports.GET("", handlers.ListExports)
			exports.POST("", handlers.CreateExport)
			exports.GET("/:id", handlers.GetExport)
			exports.GET("/:id/download", handlers.DownloadExport)
		}
	}
}

package routes

import (
	"github.com/gin-gonic/gin"

	"notification-service/handlers"
)

// SetupRouter registers health and the per-user notification API. Identity
// comes from the gateway-injected X-User-ID header.
func SetupRouter(r *gin.Engine) {
	r.GET("/healthz", handlers.HealthCheck)
	r.GET("/readyz", handlers.ReadyCheck)

	// Mesh-only: the gateway proxies /auth, /api/*, /admin — never /internal.
	r.POST("/internal/notifications", handlers.CreateInternal)

	n := r.Group("/api/notifications")
	n.Use(handlers.RequireUser())
	{
		n.GET("", handlers.ListNotifications)
		n.GET("/stream", handlers.StreamNotifications)
		n.POST("/read-all", handlers.MarkAllRead)
		n.POST("/:id/read", handlers.MarkRead)
	}
}

// Package routes maps notification-service HTTP paths to handlers.
//
// The /internal creation endpoint is how other services raise a notification;
// it is guarded by a shared secret that fails closed rather than relying on the
// gateway never routing /internal. The per-user /api group covers the feed and
// its SSE stream, and identity there comes from the AuthContext resolved by the
// auth middleware in main.go — which verifies the JWT locally by default, so a
// forged X-User-ID header grants nothing even when this service is reachable from
// something other than the gateway.
package routes

import (
	"github.com/gin-gonic/gin"

	"fyredocs/shared/authverify"
	"fyredocs/shared/config"

	"notification-service/handlers"
)

// SetupRouter registers health and the per-user notification API. Identity on
// /api comes from the verified AuthContext; /internal uses the shared secret.
func SetupRouter(r *gin.Engine) {
	r.GET("/healthz", handlers.HealthCheck)
	r.GET("/readyz", handlers.ReadyCheck)

	// Mesh-only: the gateway proxies /auth, /api/*, /admin — never /internal.
	// Guarded by the INTERNAL_API_TOKEN shared secret; fails closed (503) when
	// the token is unset so network isolation is never the only defense.
	r.POST("/internal/notifications",
		authverify.InternalTokenGin(config.GetEnv("INTERNAL_API_TOKEN", "")),
		handlers.CreateInternal)

	n := r.Group("/api/notifications")
	n.Use(handlers.RequireUser())
	{
		n.GET("", handlers.ListNotifications)
		n.GET("/stream", handlers.StreamNotifications)
		n.POST("/read-all", handlers.MarkAllRead)
		n.POST("/:id/read", handlers.MarkRead)
	}
}

// Package routes maps ai-service HTTP paths to handlers. Identity on /api
// comes from the AuthContext resolved by the auth middleware in main.go,
// which verifies the JWT locally by default — a forged X-User-ID grants
// nothing, and that identity is what AI usage is charged to.
package routes

import (
	"github.com/gin-gonic/gin"

	"ai-service/handlers"
)

// SetupRouter registers health probes and the signed-in AI op API.
func SetupRouter(r *gin.Engine, opsHandler *handlers.Ops) {
	r.GET("/healthz", handlers.HealthCheck)
	r.GET("/readyz", handlers.ReadyCheck)

	ai := r.Group("/api/ai")
	ai.Use(handlers.RequireUser())
	{
		ai.POST("/ops/:op", opsHandler.Run)
		ai.GET("/quota", opsHandler.GetQuota)
		ai.POST("/extract", opsHandler.Extract)
	}
}

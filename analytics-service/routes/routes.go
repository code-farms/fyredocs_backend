package routes

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"analytics-service/handlers"
	"esydocs/shared/response"
)

func SetupRouter(r *gin.Engine) {
	r.GET("/healthz", handlers.HealthCheck)
	r.GET("/readyz", handlers.ReadyCheck)

	admin := r.Group("/admin")
	admin.Use(adminAuth())
	{
		admin.GET("/metrics/overview", handlers.Overview)
		admin.GET("/metrics/daily", handlers.Daily)
		admin.GET("/metrics/tools", handlers.ToolUsage)
		admin.GET("/metrics/users", handlers.UserGrowth)
		admin.GET("/metrics/plans", handlers.PlanDistribution)
		admin.GET("/metrics/realtime", handlers.Realtime)
		admin.GET("/metrics/events", handlers.GetEvents)
	}
}

func adminAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		role := strings.TrimSpace(c.GetHeader("X-User-Role"))
		if role == "super-admin" {
			c.Next()
			return
		}

		userID := strings.TrimSpace(c.GetHeader("X-User-ID"))
		if userID == "" {
			response.Err(c, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
			c.Abort()
			return
		}

		response.Err(c, http.StatusForbidden, "FORBIDDEN", "Super-admin access required")
		c.Abort()
	}
}

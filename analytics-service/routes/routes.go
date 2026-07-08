package routes

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"analytics-service/handlers"
	"fyredocs/shared/response"
)

func SetupRouter(r *gin.Engine) {
	r.GET("/healthz", handlers.HealthCheck)
	r.GET("/readyz", handlers.ReadyCheck)

	// Unified, role-aware dashboard for every authenticated user. Role is
	// enforced inside the handler (admin/super-admin vs regular user), so this
	// sits outside the super-admin-only /admin group.
	r.GET("/api/dashboard", handlers.Dashboard)

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
		admin.GET("/metrics/business", handlers.BusinessMetrics)
		admin.GET("/metrics/growth", handlers.GrowthMetrics)
		admin.GET("/metrics/engagement", handlers.EngagementMetrics)
		admin.GET("/metrics/reliability", handlers.ReliabilityMetrics)
		admin.GET("/metrics/system", handlers.SystemHealth)
		admin.GET("/metrics/nats", handlers.NATSStats)
		admin.GET("/metrics/queues", handlers.QueueStatus)
		admin.GET("/metrics/server-performance", handlers.ServerPerformance)
		admin.GET("/metrics/api-performance", handlers.APIPerformance)
		admin.GET("/metrics/executive", handlers.ExecutiveOverview)
		admin.GET("/metrics/revenue", handlers.RevenueMetrics)
		admin.GET("/metrics/acquisition", handlers.AcquisitionMetrics)
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
			response.Err(c, http.StatusUnauthorized, "UNAUTHORIZED", "Please log in to access this page.")
			c.Abort()
			return
		}

		response.Err(c, http.StatusForbidden, "FORBIDDEN", "You don't have permission to access this page.")
		c.Abort()
	}
}

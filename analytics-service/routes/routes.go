// Package routes maps analytics-service HTTP paths to handlers.
//
// Three access tiers coexist here and the distinction is load-bearing: the
// role-aware /api/dashboard is open to any authenticated user (the handler
// itself decides how much to show), /admin/* is super-admin only via adminAuth,
// and the Prometheus alert receiver under /internal is deliberately
// unauthenticated because Prometheus cannot send an identity header at all.
// Moving a route between tiers changes who can read platform-wide metrics.
//
// Identity for the first two tiers comes from the AuthContext resolved by the
// auth middleware in main.go, which verifies the JWT locally by default. The
// third tier works because that middleware passes credential-less requests
// through as anonymous rather than rejecting them — so wrapping it in anything
// that demands a token would silently break Discord alerting.
package routes

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"analytics-service/handlers"
	"fyredocs/shared/authverify"
	"fyredocs/shared/response"
)

// SetupRouter registers the health probes, the role-aware user dashboard, and
// the super-admin-only /admin metrics endpoints. Admin access is gated by
// adminAuth using the gateway-injected role header.
func SetupRouter(r *gin.Engine) {
	r.GET("/healthz", handlers.HealthCheck)
	r.GET("/readyz", handlers.ReadyCheck)

	// Internal alert receiver: Prometheus POSTs firing/resolved alerts here
	// (Alertmanager v2 API) and shared/discord forwards them to Discord — this
	// replaces the standalone Alertmanager container. Unauthenticated on purpose:
	// Prometheus can't send the gateway's X-User-Role header, so it sits OUTSIDE
	// the adminAuth group; it is internal-only (Caddy never routes /internal).
	// The path matches prometheus.yml's alerting path_prefix (/internal/alerts).
	r.POST("/internal/alerts/api/v2/alerts", handlers.AlertReceiver())

	// Unified, role-aware dashboard for every authenticated user. Role is
	// enforced inside the handler (admin/super-admin vs regular user), so this
	// sits outside the super-admin-only /admin group.
	r.GET("/api/dashboard", handlers.Dashboard)

	// Activity audit trail. Ingest sits outside every auth group on purpose:
	// guest tool usage is a real audit subject, and the handler takes identity
	// exclusively from the resolved AuthContext (anonymous → guest row), never
	// from the request body. The personal feed enforces login inside the
	// handler, same tier as /api/dashboard.
	r.POST("/api/activity/events", handlers.IngestActivity)
	r.GET("/api/activity/me", handlers.GetMyActivity)

	admin := r.Group("/admin")
	admin.Use(adminAuth())
	{
		// Lightweight authorization probe. Does no work — it exists so the
		// Caddy edge can forward_auth the /grafana/* embed route through the
		// gateway and admit only super-admins (adminAuth). Must stay cheap:
		// it fires on every embedded Grafana asset request.
		admin.GET("/authz", func(c *gin.Context) {
			response.OK(c, "authorized", nil)
		})

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
		admin.GET("/metrics/api-trends", handlers.APITrends)
		admin.GET("/activity", handlers.AdminListActivity)
		admin.GET("/activity/export", handlers.AdminExportActivity)
		admin.GET("/metrics/executive", handlers.ExecutiveOverview)
		admin.GET("/metrics/revenue", handlers.RevenueMetrics)
		admin.GET("/metrics/acquisition", handlers.AcquisitionMetrics)
	}
}

// adminAuth restricts a route group to super-admins.
//
// The role comes from the AuthContext resolved by the auth middleware in main.go,
// which verifies the JWT locally by default (AUTH_TRUST_GATEWAY_HEADERS=false).
// Reading the context rather than the X-User-Role header directly is what keeps
// this sound once the service is reachable across a host boundary — otherwise
// anyone who could reach the port could simply claim `X-User-Role: super-admin`
// and read platform-wide metrics.
//
// The 401-versus-403 distinction is deliberate: no user ID means "not logged in"
// and the client should authenticate, whereas a present ID with the wrong role
// means re-authenticating will not help.
func adminAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		authCtx, _ := authverify.GetGinAuth(c)

		if strings.TrimSpace(authCtx.Role) == "super-admin" {
			c.Next()
			return
		}

		if strings.TrimSpace(authCtx.UserID) == "" {
			response.Err(c, http.StatusUnauthorized, response.CodeUnauthorized, "Please log in to access this page.")
			c.Abort()
			return
		}

		response.Err(c, http.StatusForbidden, response.CodeForbidden, "You don't have permission to access this page.")
		c.Abort()
	}
}

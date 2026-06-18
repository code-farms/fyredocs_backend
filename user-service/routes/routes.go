package routes

import (
	"github.com/gin-gonic/gin"

	"user-service/handlers"
)

// SetupRouter registers health and the organization/membership APIs. Identity
// comes from the gateway-injected X-User-ID header; org-level RBAC is enforced
// per handler from the caller's membership role.
func SetupRouter(r *gin.Engine) {
	r.GET("/healthz", handlers.HealthCheck)
	r.GET("/readyz", handlers.ReadyCheck)

	orgs := r.Group("/api/orgs")
	orgs.Use(handlers.RequireUser())
	{
		orgs.GET("", handlers.ListOrganizations)
		orgs.POST("", handlers.CreateOrganization)
		orgs.GET("/:id", handlers.GetOrganization)
		orgs.GET("/:id/members", handlers.ListMembers)
		orgs.POST("/:id/members", handlers.AddMember)
		orgs.PATCH("/:id/members/:userId", handlers.UpdateMemberRole)
		orgs.DELETE("/:id/members/:userId", handlers.RemoveMember)
	}
}

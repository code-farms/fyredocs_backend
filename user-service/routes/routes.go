// Package routes maps user-service HTTP paths to handlers.
//
// Two access tiers coexist and the distinction is load-bearing. The public
// /api/orgs routes take identity from the AuthContext resolved by the auth
// middleware in main.go, which verifies the JWT locally by default — a forged
// X-User-ID header grants nothing there. The /internal/v1 mirror exists for peer
// services, which hold no end-user JWT; it is guarded by the INTERNAL_API_TOKEN
// shared secret and only then trusts X-User-ID to name the acting user.
//
// Organization-level RBAC is not expressed here but enforced inside each handler
// from the caller's membership role, because permission depends on the target
// organization rather than on the route.
package routes

import (
	"github.com/gin-gonic/gin"

	"fyredocs/shared/authverify"
	"fyredocs/shared/config"

	"user-service/handlers"
)

// SetupRouter registers health, the public organization/membership APIs, and the
// internal membership lookup used by peer services.
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

	// Mesh-only membership lookup. document-service calls this to resolve a
	// caller's org role when authorizing documents; it has the user's ID but no
	// JWT to forward, so the public route above would reject it. Guarded by the
	// shared secret, which fails closed (503) when INTERNAL_API_TOKEN is unset.
	//
	// NOTE: consumed by document-service/internal/orgclient/orgclient.go — the
	// path and the "role" field in GetOrganization's payload are a contract with
	// that client. Caddy never routes /internal, so this is unreachable publicly.
	internal := r.Group("/internal/v1",
		authverify.InternalTokenGin(config.GetEnv("INTERNAL_API_TOKEN", "")),
		handlers.InternalIdentity(),
	)
	{
		internal.GET("/orgs/:id", handlers.GetOrganization)
	}
}

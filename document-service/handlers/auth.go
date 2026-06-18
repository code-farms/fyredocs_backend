package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"fyredocs/shared/response"
)

// userID extracts the authenticated user from the gateway-injected X-User-ID
// header. The gateway verifies the JWT and sets this header on proxied
// requests, so it is trusted here (same model as analytics-service).
func userID(c *gin.Context) (uuid.UUID, bool) {
	raw := strings.TrimSpace(c.GetHeader("X-User-ID"))
	if raw == "" {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// RequireUser aborts unauthenticated requests.
func RequireUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := userID(c); !ok {
			response.Err(c, http.StatusUnauthorized, "UNAUTHORIZED", "Please log in to access your documents.")
			c.Abort()
			return
		}
		c.Next()
	}
}

// parseUUID is a small helper for optional UUID string fields.
func parseUUID(s *string) (*uuid.UUID, bool) {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil, true // absent is valid (no value)
	}
	id, err := uuid.Parse(strings.TrimSpace(*s))
	if err != nil {
		return nil, false
	}
	return &id, true
}

// Organization RBAC ranks (mirrored from user-service; cross-service code is
// not shared per repo rules). Reads need viewer+, writes need editor+.
var orgRoleRank = map[string]int{"viewer": 1, "editor": 2, "admin": 3, "owner": 4}

func roleAtLeast(role, min string) bool {
	return orgRoleRank[role] > 0 && orgRoleRank[role] >= orgRoleRank[min]
}

func queryInt(c *gin.Context, key string, fallback int) int {
	return parseQueryInt(c.Query(key), fallback)
}

func parseQueryInt(val string, fallback int) int {
	if val == "" {
		return fallback
	}
	n := 0
	for _, ch := range val {
		if ch < '0' || ch > '9' {
			return fallback
		}
		n = n*10 + int(ch-'0')
	}
	if n <= 0 {
		return fallback
	}
	return n
}

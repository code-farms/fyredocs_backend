package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"fyredocs/shared/authverify"
	"fyredocs/shared/response"
)

// userID extracts the authenticated user from the AuthContext resolved by the
// auth middleware.
//
// Reading the context rather than the X-User-ID header directly is what makes
// this safe once the service is reachable across a host boundary: with
// AUTH_TRUST_GATEWAY_HEADERS off (the default) the context is populated only from
// a verified JWT, so a forged X-User-ID header resolves to no identity at all.
// The accessor works unchanged when the flag is on, because the middleware
// populates the same context from gateway headers in that mode.
func userID(c *gin.Context) (uuid.UUID, bool) {
	authCtx, ok := authverify.GetGinAuth(c)
	if !ok {
		return uuid.Nil, false
	}
	raw := strings.TrimSpace(authCtx.UserID)
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
			response.Err(c, http.StatusUnauthorized, response.CodeUnauthorized, "Please log in to access your documents.")
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

// roleAtLeast compares organization roles by rank, so authorization checks can
// express a minimum privilege instead of enumerating every acceptable role — which
// is what makes adding a role a one-line change rather than a sweep.
func roleAtLeast(role, min string) bool {
	return orgRoleRank[role] > 0 && orgRoleRank[role] >= orgRoleRank[min]
}

// queryInt reads an integer query parameter with a fallback.
func queryInt(c *gin.Context, key string, fallback int) int {
	return parseQueryInt(c.Query(key), fallback)
}

// parseQueryInt parses an integer query value, reporting whether it was usable so
// callers can distinguish "absent" from "present but invalid".
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

package handlers

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"fyredocs/shared/authverify"
	"fyredocs/shared/logger"
	"fyredocs/shared/response"

	"user-service/internal/models"
)

// userID extracts the authenticated user from the AuthContext resolved upstream.
//
// Reading the context rather than the X-User-ID header directly is what makes
// this safe once the service is reachable across a host boundary: with
// AUTH_TRUST_GATEWAY_HEADERS off (the default) the context is populated only from
// a verified JWT, so a forged X-User-ID header resolves to no identity at all.
// The accessor works unchanged when the flag is on, and on the /internal/v1
// routes where InternalIdentity populates the context behind the shared secret.
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

// InternalIdentity names the acting user from the X-User-ID header for
// service-to-service calls, which carry no end-user JWT.
//
// NOTE: this MUST only be mounted behind authverify.InternalTokenGin (see
// routes/routes.go). The shared secret is what proves the caller is a peer
// service rather than a browser; without that guard this middleware would let
// any caller act as any user. The public /api routes deliberately do NOT use it —
// there identity comes from a verified JWT (see main.go buildAuthMiddleware).
func InternalIdentity() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := strings.TrimSpace(c.GetHeader("X-User-ID"))
		if raw == "" {
			response.Err(c, http.StatusBadRequest, response.CodeInvalidInput, "X-User-ID is required.")
			c.Abort()
			return
		}
		authverify.SetGinAuth(c, authverify.AuthContext{UserID: raw})
		c.Next()
	}
}

// RequireUser aborts unauthenticated requests.
func RequireUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := userID(c); !ok {
			response.Err(c, http.StatusUnauthorized, response.CodeUnauthorized, "Please log in.")
			c.Abort()
			return
		}
		c.Next()
	}
}

// membershipRole returns the caller's role in an org, or ("", false) if they
// are not a member. A real DB error (not just "not found") is logged so a
// connection failure isn't silently treated as "not a member".
func membershipRole(ctx context.Context, orgID, uid uuid.UUID) (string, bool) {
	var m models.Membership
	if err := models.DB.Where("organization_id = ? AND user_id = ?", orgID, uid).First(&m).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			logger.LogWarn(ctx, "db.membership.lookup", err, "orgId", orgID, "userId", uid)
		}
		return "", false
	}
	return m.Role, true
}

// slugNonAlnum matches characters stripped when deriving an organization slug.
// Compiled once at package level — recompiling per call would be needless work on
// a request path.
var slugNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// slugify produces a URL-safe slug; callers append a uniqueness suffix.
func slugify(name string) string {
	s := slugNonAlnum.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "org"
	}
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

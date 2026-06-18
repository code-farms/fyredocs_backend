package handlers

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"fyredocs/shared/response"

	"user-service/internal/models"
)

// userID extracts the authenticated user from the gateway-injected header.
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
			response.Err(c, http.StatusUnauthorized, "UNAUTHORIZED", "Please log in.")
			c.Abort()
			return
		}
		c.Next()
	}
}

// membershipRole returns the caller's role in an org, or ("", false) if they
// are not a member.
func membershipRole(orgID, uid uuid.UUID) (string, bool) {
	var m models.Membership
	if err := models.DB.Where("organization_id = ? AND user_id = ?", orgID, uid).First(&m).Error; err != nil {
		return "", false
	}
	return m.Role, true
}

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

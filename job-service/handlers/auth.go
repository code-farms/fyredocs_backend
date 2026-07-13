package handlers

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"fyredocs/shared/authverify"
)

// authUserID returns the authenticated user's ID from the JWT-verified auth
// context, or nil when the request is unauthenticated. It deliberately ignores
// the client-supplied X-User-ID header so identity can only come from a
// verified token.
func authUserID(c *gin.Context) *uuid.UUID {
	if c == nil {
		return nil
	}
	if authCtx, ok := authverify.GetGinAuth(c); ok && authCtx.UserID != "" {
		if parsed, err := uuid.Parse(strings.TrimSpace(authCtx.UserID)); err == nil {
			return &parsed
		}
	}
	return nil
}

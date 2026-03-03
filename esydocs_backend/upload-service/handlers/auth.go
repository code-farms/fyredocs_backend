package handlers

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"esydocs/shared/auth"
)

// Fix #3: Removed X-User-ID header fallback - only trust auth context from JWT
func authUserID(c *gin.Context) *uuid.UUID {
	if c == nil {
		return nil
	}
	if authCtx, ok := auth.GetGinAuth(c); ok && authCtx.UserID != "" {
		if parsed, err := uuid.Parse(strings.TrimSpace(authCtx.UserID)); err == nil {
			return &parsed
		}
	}
	return nil
}

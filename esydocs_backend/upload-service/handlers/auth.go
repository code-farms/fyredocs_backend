package handlers

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"upload-service/auth"
)

func authUserID(c *gin.Context) *uuid.UUID {
	if c == nil {
		return nil
	}
	if authCtx, ok := auth.GetGinAuth(c); ok && authCtx.UserID != "" {
		if parsed, err := uuid.Parse(strings.TrimSpace(authCtx.UserID)); err == nil {
			return &parsed
		}
	}
	if header := c.GetHeader("X-User-ID"); header != "" {
		if parsed, err := uuid.Parse(strings.TrimSpace(header)); err == nil {
			return &parsed
		}
	}
	return nil
}

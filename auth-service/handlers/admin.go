package handlers

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"auth-service/internal/models"

	"fyredocs/shared/response"
)

// RevokeUserSessions revokes all active sessions for a user.
// POST /internal/users/:id/revoke-sessions
func (ae *AuthEndpoints) RevokeUserSessions(c *gin.Context) {
	userIDStr := c.Param("id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		response.BadRequest(c, "INVALID_INPUT", "Invalid user ID.")
		return
	}

	sessions, err := models.RevokeAllUserSessions(models.DB, userID)
	if err != nil {
		response.InternalError(c, "SERVER_ERROR", "Failed to revoke sessions.")
		return
	}

	// Add each token to the Redis denylist for cross-service invalidation
	if ae.Denylist != nil {
		ctx := c.Request.Context()
		for _, s := range sessions {
			remaining := time.Until(s.AccessExpiresAt)
			if remaining > 0 {
				if err := ae.Denylist.DenyToken(ctx, s.AccessTokenHash, remaining); err != nil {
					slog.Warn("failed to deny token in redis", "error", err, "sessionId", s.ID)
				}
			}
		}
	}

	response.OK(c, "Sessions revoked", gin.H{
		"revokedCount": len(sessions),
	})
}

// RevokeSession revokes a single session by ID.
// DELETE /internal/sessions/:id
func (ae *AuthEndpoints) RevokeSession(c *gin.Context) {
	sessionIDStr := c.Param("id")
	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		response.BadRequest(c, "INVALID_INPUT", "Invalid session ID.")
		return
	}

	var session models.UserSession
	if err := models.DB.First(&session, "id = ?", sessionID).Error; err != nil {
		response.Err(c, http.StatusNotFound, "NOT_FOUND", "Session not found.")
		return
	}

	if err := models.DB.Delete(&session).Error; err != nil {
		response.InternalError(c, "SERVER_ERROR", "Failed to revoke session.")
		return
	}

	// Add to Redis denylist for cross-service invalidation
	if ae.Denylist != nil {
		remaining := time.Until(session.AccessExpiresAt)
		if remaining > 0 {
			ctx := c.Request.Context()
			if err := ae.Denylist.DenyToken(ctx, session.AccessTokenHash, remaining); err != nil {
				slog.Warn("failed to deny token in redis", "error", err, "sessionId", session.ID)
			}
		}
	}

	response.OK(c, "Session revoked", nil)
}

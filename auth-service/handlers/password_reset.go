package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"auth-service/internal/models"

	"fyredocs/shared/config"
	"fyredocs/shared/logger"
	"fyredocs/shared/response"
)

type forgotPasswordRequest struct {
	Email string `json:"email"`
}

type resetPasswordRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"newPassword"`
}

// forgotPasswordSuccessMsg is intentionally generic so the response is identical
// whether or not the email exists in the database. This prevents account enumeration.
const forgotPasswordSuccessMsg = "If an account exists for this email, a reset link has been sent."

// ForgotPassword issues a single-use password reset token, emails it, and always
// responds 200 regardless of whether the email is registered.
//
// POST /auth/forgot-password
func (ae *AuthEndpoints) ForgotPassword(c *gin.Context) {
	var payload forgotPasswordRequest
	if err := c.ShouldBindJSON(&payload); err != nil {
		// Even malformed JSON returns 200 to preserve no-enumeration UX. We log
		// the parse failure so legitimate client bugs are still observable.
		logger.LogWarn(c.Request.Context(), "forgot_password.bind", err)
		response.OK(c, forgotPasswordSuccessMsg, nil)
		return
	}

	email := normalizeEmail(payload.Email)
	if email == "" {
		response.OK(c, forgotPasswordSuccessMsg, nil)
		return
	}

	var user models.User
	if err := models.DB.Where("email = ?", email).First(&user).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			logger.LogWarn(c.Request.Context(), "forgot_password.user_lookup", err, "email", email)
		}
		response.OK(c, forgotPasswordSuccessMsg, nil)
		return
	}

	ttl := config.GetEnvDuration("PASSWORD_RESET_TOKEN_TTL", 1*time.Hour)

	if err := models.DeleteResetTokensForUser(models.DB, user.ID); err != nil {
		logger.LogWarn(c.Request.Context(), "forgot_password.delete_existing_tokens", err, "userId", user.ID)
		// Fall through — we still want to attempt to issue a new token.
	}

	rawToken, err := generateResetToken()
	if err != nil {
		response.InternalErrorf(c, "SERVER_ERROR", "Could not start password reset. Please try again.", err,
			"op", "generate_reset_token", "userId", user.ID)
		return
	}

	if err := models.CreatePasswordResetToken(models.DB, user.ID, rawToken, ttl, c.ClientIP()); err != nil {
		response.InternalErrorf(c, "SERVER_ERROR", "Could not start password reset. Please try again.", err,
			"op", "db.password_reset_tokens.create", "userId", user.ID)
		return
	}

	appBaseURL := strings.TrimRight(config.GetEnv("APP_BASE_URL", "http://localhost:5173"), "/")
	resetURL := appBaseURL + "/reset-password?token=" + rawToken

	slog.Info("password_reset.requested", "userId", user.ID.String())

	if ae.Mailer != nil {
		// Fire-and-forget — mailer latency must not affect response time or
		// observable behaviour (would leak whether the email exists).
		go func(to, url, ip string, d time.Duration) {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("password_reset.mailer_panic", "panic", r, "userId", user.ID.String())
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := ae.Mailer.SendPasswordReset(ctx, to, url, ip, d); err != nil {
				slog.Warn("password_reset.mailer_send_failed", "error", err, "userId", user.ID.String())
			}
		}(user.Email, resetURL, c.ClientIP(), ttl)
	}

	response.OK(c, forgotPasswordSuccessMsg, nil)
}

// ResetPassword consumes a reset token, updates the password hash, revokes
// every session for the user, and denylists their outstanding access tokens.
//
// POST /auth/reset-password
func (ae *AuthEndpoints) ResetPassword(c *gin.Context) {
	var payload resetPasswordRequest
	if err := c.ShouldBindJSON(&payload); err != nil {
		response.Errorf(c, http.StatusBadRequest, "INVALID_INPUT", "Invalid request. Please try again.", err,
			"op", "bind_reset_password_payload")
		return
	}

	token := strings.TrimSpace(payload.Token)
	if token == "" {
		response.Err(c, http.StatusBadRequest, "INVALID_INPUT", "Reset token is required.")
		return
	}

	// Mirror the password length rules enforced in Signup.
	if len(payload.NewPassword) < 8 {
		response.Err(c, http.StatusBadRequest, "WEAK_PASSWORD", "Password must be at least 8 characters")
		return
	}
	if len(payload.NewPassword) > 128 {
		response.Err(c, http.StatusBadRequest, "INVALID_INPUT", "Password must not exceed 128 characters")
		return
	}

	row, err := models.FindValidResetTokenByHash(models.DB, models.HashToken(token))
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			logger.LogWarn(c.Request.Context(), "reset_password.find_token", err)
		}
		response.Err(c, http.StatusBadRequest, "INVALID_OR_EXPIRED_TOKEN",
			"This reset link is invalid or has expired. Please request a new one.")
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(payload.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		response.InternalErrorf(c, "SERVER_ERROR", "Could not update your password. Please try again.", err,
			"op", "bcrypt.generate_password_hash", "userId", row.UserID)
		return
	}

	var revokedSessions []models.UserSession
	txErr := models.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.User{}).Where("id = ?", row.UserID).Update("password_hash", string(passwordHash)).Error; err != nil {
			return err
		}
		if err := models.DeleteResetTokensForUser(tx, row.UserID); err != nil {
			return err
		}
		sessions, err := models.RevokeAllUserSessions(tx, row.UserID)
		if err != nil {
			return err
		}
		revokedSessions = sessions
		return nil
	})
	if txErr != nil {
		response.InternalErrorf(c, "SERVER_ERROR", "Could not update your password. Please try again.", txErr,
			"op", "db.password_reset.commit", "userId", row.UserID)
		return
	}

	// Add each revoked session's access token to the Redis denylist so other
	// services treat the token as invalid until it expires naturally. Mirrors
	// the denylist step in the admin session-revocation handler.
	if ae.Denylist != nil {
		ctx := c.Request.Context()
		for _, s := range revokedSessions {
			remaining := time.Until(s.AccessExpiresAt)
			if remaining > 0 {
				if err := ae.Denylist.DenyToken(ctx, s.AccessTokenHash, remaining); err != nil {
					slog.Warn("password_reset.deny_token_failed", "error", err, "sessionId", s.ID)
				}
			}
		}
	}

	// Drop the plan cache so a fresh login re-populates it.
	ae.deletePlanCache(c.Request.Context(), row.UserID.String())

	publishAnalyticsEvent(c.Request.Context(), "user.password_reset_completed", row.UserID.String(), "")

	slog.Info("password_reset.completed",
		"userId", row.UserID.String(),
		"revokedSessions", len(revokedSessions),
	)

	response.OK(c, "Password updated. Please sign in.", nil)
}

// generateResetToken returns 32 cryptographically-random bytes as a base64url
// string with no padding. ~43 characters, URL-safe.
func generateResetToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

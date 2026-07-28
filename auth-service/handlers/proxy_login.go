package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"auth-service/internal/models"
	"fyredocs/shared/authverify"

	"fyredocs/shared/config"
	"fyredocs/shared/logger"
	"fyredocs/shared/natsconn"
	"fyredocs/shared/queue"
	"fyredocs/shared/response"
)

// proxyLoginAllowedRoles are the caller roles permitted to impersonate users.
var proxyLoginAllowedRoles = map[string]bool{
	"admin":       true,
	"super-admin": true,
}

// proxyLoginRequest names the user an admin wishes to impersonate.
//
// This is the most sensitive request shape in the service: honouring it mints a
// real session for somebody else. Authorization is enforced in the handler, not
// here, and the endpoint carries its own dedicated rate limit.
type proxyLoginRequest struct {
	UserID string `json:"userId" binding:"required,uuid"`
}

// ProxyLogin lets an admin/super-admin mint a short-lived access token for
// another user (impersonation). The issued token carries an impersonated_by
// claim, has a short TTL, and is NOT paired with a refresh token, so it cannot
// be silently renewed. The action is audit-logged and published as an event.
//
// POST /auth/proxy-login
func (ae *AuthEndpoints) ProxyLogin(c *gin.Context) {
	if !config.GetEnvBool("PROXY_LOGIN_ENABLED", true) {
		response.Forbidden(c, "PROXY_LOGIN_DISABLED", "Proxy login is disabled.")
		return
	}

	authCtx, ok := authverify.GetGinAuth(c)
	if !ok || strings.TrimSpace(authCtx.UserID) == "" {
		response.Unauthorized(c, response.CodeUnauthorized, "Your session has expired. Please log in again.")
		return
	}

	// Fast-path gate on the JWT's claimed role; the authoritative check runs
	// against the database below, after the request is validated.
	if !proxyLoginAllowedRoles[strings.TrimSpace(authCtx.Role)] {
		response.Forbidden(c, response.CodeForbidden, "You are not allowed to impersonate users.")
		return
	}

	var body proxyLoginRequest
	if !response.BindJSON(c, &body) {
		return
	}

	targetID, err := uuid.Parse(strings.TrimSpace(body.UserID))
	if err != nil {
		response.Errorf(c, http.StatusBadRequest, response.CodeInvalidInput, "Please provide a valid target user ID.", err,
			"op", "parse_target_user_id", "adminId", authCtx.UserID, "targetUserId", body.UserID)
		return
	}

	if targetID.String() == strings.TrimSpace(authCtx.UserID) {
		response.BadRequest(c, "INVALID_TARGET", "You cannot impersonate yourself.")
		return
	}

	// Authoritative role check: the caller's role is re-read from the database
	// rather than trusted from the JWT — a demoted admin keeps their old role
	// claim until the token expires, and impersonation is too sensitive to
	// honor a stale claim.
	var caller models.User
	if err := models.DB.First(&caller, "id = ?", strings.TrimSpace(authCtx.UserID)).Error; err != nil {
		response.InternalErrorf(c, response.CodeServerError, "Proxy login failed. Please try again.", err,
			"op", "db.users.lookup_caller", "adminId", authCtx.UserID)
		return
	}
	if !proxyLoginAllowedRoles[strings.TrimSpace(caller.Role)] {
		response.Forbidden(c, response.CodeForbidden, "You are not allowed to impersonate users.")
		return
	}

	var target models.User
	if err := models.DB.First(&target, "id = ?", targetID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Err(c, http.StatusNotFound, "USER_NOT_FOUND", "The target user does not exist.")
			return
		}
		response.InternalErrorf(c, response.CodeServerError, "Proxy login failed. Please try again.", err,
			"op", "db.users.lookup_target", "adminId", authCtx.UserID, "targetUserId", targetID)
		return
	}

	// Role-rank check: impersonation requires strictly greater privilege, so an
	// admin can never obtain a super-admin token by impersonating one (privilege
	// escalation), and equal-rank impersonation is refused to protect peer
	// accounts.
	if !models.CanImpersonate(caller.Role, target.Role) {
		slog.Warn("proxy_login.rank_denied",
			"adminId", authCtx.UserID, "callerRole", caller.Role,
			"targetUserId", target.ID, "targetRole", target.Role)
		response.Forbidden(c, response.CodeForbidden, "You cannot impersonate a user with an equal or higher role.")
		return
	}

	ae.respondWithImpersonationToken(c, target, authCtx.UserID)
}

// respondWithImpersonationToken issues a short-lived, refresh-less access token
// for the target user and sets it as the access cookie. Mirrors
// respondWithTokens but omits the refresh token entirely.
func (ae *AuthEndpoints) respondWithImpersonationToken(c *gin.Context, target models.User, adminID string) {
	var plan models.SubscriptionPlan
	if err := models.DB.Where("name = ?", target.PlanName).First(&plan).Error; err != nil {
		plan = models.SubscriptionPlan{Name: "free", MaxFileSizeMB: 25, MaxFilesPerJob: 10, RetentionDays: 7}
	}

	role := target.Role
	if role == "" {
		role = "user"
	}

	ttl := config.GetEnvDuration("PROXY_LOGIN_TTL", 30*time.Minute)

	// Cache plan info in Redis for the API gateway to read.
	ae.cachePlanInfo(c.Request.Context(), target.ID.String(), plan, ttl)

	accessToken, jti, accessExpiresAt, err := ae.Issuer.IssueImpersonationToken(target.ID.String(), role, nil, adminID, ttl)
	if err != nil {
		response.InternalErrorf(c, response.CodeServerError, "Proxy login failed. Please try again.", err,
			"op", "issue_impersonation_token", "adminId", adminID, "targetUserId", target.ID)
		return
	}

	sessionID, err := uuid.Parse(jti)
	if err != nil {
		response.InternalErrorf(c, response.CodeServerError, "Proxy login failed. Please try again.", err,
			"op", "parse_session_jti", "jti", jti, "adminId", adminID, "targetUserId", target.ID)
		return
	}

	if err := models.StoreAccessOnlySession(models.DB, sessionID, target.ID, accessToken, accessExpiresAt); err != nil {
		slog.Warn("failed to store impersonation session in database", "error", err,
			"adminId", adminID, "targetUserId", target.ID)
	}

	// Only the access cookie is set — no refresh cookie — so the impersonation
	// naturally expires and cannot be renewed via /auth/refresh.
	setAccessTokenCookie(c, accessToken, ttl)

	logProxyLogin(c.Request.Context(), adminID, target.ID.String(), role)

	response.OK(c, "Impersonation session started.", gin.H{
		"user":            buildUserResponse(target, role, plan),
		"accessExpiresAt": accessExpiresAt.UnixMilli(),
		"impersonatedBy":  adminID,
	})
}

// logProxyLogin records the impersonation for audit purposes via structured
// logs and, when NATS is available, an analytics event.
func logProxyLogin(ctx context.Context, adminID, targetUserID, targetRole string) {
	slog.Info("proxy_login",
		"adminId", adminID,
		"targetUserId", targetUserID,
		"targetRole", targetRole,
	)

	if natsconn.JS == nil {
		return
	}
	metadata, _ := json.Marshal(map[string]string{
		"adminId":    adminID,
		"targetRole": targetRole,
	})
	event := queue.AnalyticsEvent{
		EventType: "user.proxy_login",
		UserID:    targetUserID,
		Metadata:  metadata,
		Timestamp: time.Now().UTC(),
	}
	if err := queue.PublishAnalyticsEvent(ctx, natsconn.JS, event); err != nil {
		logger.LogWarn(ctx, "publish_proxy_login_event", err, "adminId", adminID, "targetUserId", targetUserID)
	}
}

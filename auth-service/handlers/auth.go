package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"auth-service/internal/email"
	"auth-service/internal/models"
	"auth-service/internal/token"
	"fyredocs/shared/authverify"

	"fyredocs/shared/config"
	"fyredocs/shared/logger"
	"fyredocs/shared/natsconn"
	"fyredocs/shared/queue"
	"fyredocs/shared/response"
)

// signupRequest is the inbound signup payload. Validation lives in the binding
// tags (funneled through response.BindJSON); handlers only normalize
// (trim/lowercase) afterwards.
type signupRequest struct {
	Email    string `json:"email" binding:"required,email,max=254"`
	Password string `json:"password" binding:"required,min=8,max=128"`
	FullName string `json:"fullName" binding:"required,max=100"`
	Phone    string `json:"phone" binding:"omitempty,max=32"`
	Country  string `json:"country" binding:"required,max=100"`
	Image    string `json:"image" binding:"omitempty,max=1024"`
}

// loginRequest is the inbound login payload. Deliberately laxer than signup
// (no email-format or min-length rules beyond presence): login must accept
// whatever credentials an existing account might have, and reject them with
// the uniform INVALID_CREDENTIALS response, not a validation error.
type loginRequest struct {
	Email    string `json:"email" binding:"required,max=254"`
	Password string `json:"password" binding:"required,max=128"`
}

// authUser is the sanitized user representation returned to clients; it never
// carries the password hash.
type authUser struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	FullName string `json:"fullName"`
	Phone    string `json:"phone,omitempty"`
	Country  string `json:"country"`
	Image    string `json:"image,omitempty"`
	Role     string `json:"role,omitempty"`
	PlanName string `json:"planName,omitempty"`
}

// AuthEndpoints groups the dependencies shared by the authentication handlers:
// the token issuer, the cross-service token denylist, the Redis client used for
// the plan cache, and the transactional mailer.
type AuthEndpoints struct {
	Issuer      *token.Issuer
	Denylist    authverify.TokenDenylist
	RedisClient *redis.Client
	Mailer      email.Mailer
}

// Signup registers a new account. It validates and normalizes the payload,
// rejects duplicate emails, hashes the password with bcrypt, persists the user,
// emits a signup analytics event, and responds with a fresh token pair.
func (ae *AuthEndpoints) Signup(c *gin.Context) {
	var payload signupRequest
	if !response.BindJSON(c, &payload) {
		return
	}

	email := normalizeEmail(payload.Email)
	fullName := strings.TrimSpace(payload.FullName)
	country := strings.TrimSpace(payload.Country)
	phone := strings.TrimSpace(payload.Phone)
	image := strings.TrimSpace(payload.Image)

	// Binding tags cover format/length; whitespace-only values still need the
	// post-trim check (validator does not trim).
	if email == "" || strings.TrimSpace(payload.Password) == "" || fullName == "" || country == "" {
		response.Err(c, http.StatusBadRequest, response.CodeInvalidInput, "Please fill in all required fields.")
		return
	}

	var existing models.User
	if err := models.DB.Where("email = ?", email).First(&existing).Error; err == nil {
		response.Err(c, http.StatusConflict, "USER_ALREADY_EXISTS", "An account with this email already exists. Please log in instead.")
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		response.InternalErrorf(c, response.CodeServerError, "Could not create your account. Please try again.", err,
			"op", "db.users.lookup_existing", "email", email)
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(payload.Password), bcrypt.DefaultCost)
	if err != nil {
		response.InternalErrorf(c, response.CodeServerError, "Could not create your account. Please try again.", err,
			"op", "bcrypt.generate_password_hash")
		return
	}

	user := models.User{
		Email:        email,
		FullName:     fullName,
		Phone:        phone,
		Country:      country,
		ImageURL:     image,
		PasswordHash: string(passwordHash),
	}
	if err := models.DB.Create(&user).Error; err != nil {
		if isDuplicateError(err) {
			response.Err(c, http.StatusConflict, "USER_ALREADY_EXISTS", "An account with this email already exists. Please log in instead.")
			return
		}
		response.InternalErrorf(c, response.CodeServerError, "Could not create your account. Please try again.", err,
			"op", "db.users.create", "email", email)
		return
	}

	publishAnalyticsEvent(c.Request.Context(), "user.signup", user.ID.String(), user.PlanName)
	ae.respondWithTokens(c, user)
}

// dummyPasswordHash is compared against when the email is not registered so
// the unknown-email path costs the same bcrypt work as a real password check.
// Without it, an unknown email returns in ~1ms vs ~100ms+ for a known one — a
// timing oracle for account enumeration. Generated at startup so it always
// matches the current bcrypt.DefaultCost.
var dummyPasswordHash = func() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte("fyredocs-timing-equalizer"), bcrypt.DefaultCost)
	if err != nil {
		// bcrypt.GenerateFromPassword only fails on an invalid cost; DefaultCost
		// is always valid, so this is unreachable in practice.
		panic(err)
	}
	return h
}()

// Login authenticates a user by email and password and, on success, issues a
// fresh token pair. All failure paths return the same generic message AND burn
// the same bcrypt cost, so neither the response nor its timing leaks whether an
// email is registered (account enumeration).
func (ae *AuthEndpoints) Login(c *gin.Context) {
	var payload loginRequest
	if !response.BindJSON(c, &payload) {
		return
	}

	email := normalizeEmail(payload.Email)
	if email == "" || strings.TrimSpace(payload.Password) == "" {
		response.Unauthorized(c, "INVALID_CREDENTIALS", "Incorrect email or password. Please try again.")
		return
	}

	var user models.User
	if err := models.DB.Where("email = ?", email).First(&user).Error; err != nil {
		// Burn the same bcrypt cost as the password-mismatch path (see
		// dummyPasswordHash). The log line deliberately omits the email — it is
		// attacker-supplied PII on this path.
		_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(payload.Password))
		logger.LogWarn(c.Request.Context(), "login.user_lookup", err)
		response.Unauthorized(c, "INVALID_CREDENTIALS", "Incorrect email or password. Please try again.")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(payload.Password)); err != nil {
		logger.LogWarn(c.Request.Context(), "login.password_mismatch", err, "userId", user.ID)
		response.Unauthorized(c, "INVALID_CREDENTIALS", "Incorrect email or password. Please try again.")
		return
	}

	publishAnalyticsEvent(c.Request.Context(), "user.login", user.ID.String(), user.PlanName)
	ae.respondWithTokens(c, user)
}

// Refresh rotates the token pair using the refresh-token cookie. Every
// successful refresh issues a NEW refresh token and retires the presented one
// (rotation); a replay of a retired token outside a short grace window is
// treated as theft — the session is revoked, its access token denylisted, and
// the cookies cleared (reuse detection). Concurrent refreshes from multiple
// tabs are absorbed by the grace window: the loser gets a fresh access token
// but no second rotation. All failures return the same generic 401 so the
// endpoint leaks nothing about why a token was rejected.
func (ae *AuthEndpoints) Refresh(c *gin.Context) {
	cookieName := config.GetEnv("AUTH_REFRESH_COOKIE", "refresh_token")
	refreshToken, err := c.Cookie(cookieName)
	if err != nil || strings.TrimSpace(refreshToken) == "" {
		if err != nil {
			logger.LogWarn(c.Request.Context(), "refresh.read_cookie", err, "cookie", cookieName)
		}
		respondRefreshInvalid(c)
		return
	}

	userID, err := ae.Issuer.VerifyRefreshToken(refreshToken)
	if err != nil {
		logger.LogWarn(c.Request.Context(), "refresh.verify_token", err)
		respondRefreshInvalid(c)
		return
	}

	parsedID, err := uuid.Parse(strings.TrimSpace(userID))
	if err != nil {
		logger.LogErr(c.Request.Context(), "refresh.parse_user_id", err, "userId", userID)
		respondRefreshInvalid(c)
		return
	}

	refreshHash := models.HashToken(refreshToken)
	now := time.Now()
	grace := config.GetEnvDuration("AUTH_REFRESH_ROTATE_GRACE", 30*time.Second)

	session, activeMatch, prevMatch, ok := findRefreshSession(c, refreshHash, userID)
	if !ok {
		return
	}

	if session != nil && session.UserID != parsedID {
		// The session row and the token's subject disagree — never proceed.
		logger.LogErr(c.Request.Context(), "refresh.session_user_mismatch", nil,
			"sessionUserId", session.UserID, "tokenUserId", parsedID)
		respondRefreshInvalid(c)
		return
	}

	var revokedAt, rotatedAt *time.Time
	if session != nil {
		revokedAt, rotatedAt = session.RevokedAt, session.RotatedAt
	}
	outcome := models.ClassifyRefresh(activeMatch, prevMatch, revokedAt, rotatedAt, now, grace)

	switch outcome {
	case models.OutcomeInvalid:
		respondRefreshInvalid(c)
		return
	case models.OutcomeReuse:
		ae.handleRefreshReuse(c, session)
		return
	}

	var user models.User
	if err := models.DB.First(&user, "id = ?", parsedID).Error; err != nil {
		logger.LogWarn(c.Request.Context(), "refresh.lookup_user", err, "userId", parsedID)
		respondRefreshInvalid(c)
		return
	}

	plan, planOK := lookupPlan(c.Request.Context(), user.PlanName)
	if !planOK {
		plan = models.SubscriptionPlan{Name: "free", MaxFileSizeMB: 25, MaxFilesPerJob: 10, RetentionDays: 7}
	}

	role := user.Role
	if role == "" {
		role = "user"
	}

	accessTTL := config.GetEnvDuration("JWT_ACCESS_TTL", 8*time.Hour)

	ae.cachePlanInfo(c.Request.Context(), user.ID.String(), plan, accessTTL)

	accessToken, _, accessExpiresAt, err := ae.Issuer.IssueAccessToken(user.ID.String(), role, nil, accessTTL)
	if err != nil {
		response.InternalErrorf(c, response.CodeServerError, "Failed to refresh token. Please try again.", err,
			"op", "issue_access_token.refresh", "userId", user.ID)
		return
	}

	if outcome == models.OutcomeRotate {
		// Rotation never extends the session's absolute lifetime: the new
		// refresh expiry is capped at the original RefreshExpiresAt, so a stolen
		// cookie cannot keep a session alive forever by refreshing.
		refreshTTL := config.GetEnvDuration("JWT_REFRESH_TTL", 7*24*time.Hour)
		newRefreshExpiresAt := now.Add(refreshTTL)
		if session.RefreshExpiresAt != nil && session.RefreshExpiresAt.Before(newRefreshExpiresAt) {
			newRefreshExpiresAt = *session.RefreshExpiresAt
		}

		newRefreshToken, _, _, err := ae.Issuer.IssueRefreshToken(user.ID.String(), newRefreshExpiresAt.Sub(now))
		if err != nil {
			response.InternalErrorf(c, response.CodeServerError, "Failed to refresh token. Please try again.", err,
				"op", "issue_refresh_token.refresh", "userId", user.ID)
			return
		}

		rotated, err := models.RotateSessionTokens(models.DB, session.ID, refreshHash,
			models.HashToken(accessToken), accessExpiresAt,
			models.HashToken(newRefreshToken), newRefreshExpiresAt)
		if err != nil {
			// Without the persisted rotation the new refresh cookie would be
			// unusable on the next refresh — fail loudly instead of half-rotating.
			response.InternalErrorf(c, response.CodeServerError, "Failed to refresh token. Please try again.", err,
				"op", "rotate_session_tokens", "userId", user.ID, "sessionId", session.ID)
			return
		}
		if rotated {
			setRefreshTokenCookie(c, newRefreshToken, newRefreshExpiresAt.Sub(now))
		} else {
			// A concurrent refresh won the optimistic-guard race after our
			// lookup. The winner holds the rotated pair; this response degrades
			// to the grace behavior (fresh access token, no refresh cookie).
			logger.LogWarn(c.Request.Context(), "refresh.rotation_race_lost", nil,
				"sessionId", session.ID, "userId", user.ID)
		}
	}
	// OutcomeGrace: the session row is left untouched and no refresh cookie is
	// set — the winning request's cookie is authoritative.

	setAccessTokenCookie(c, accessToken, accessTTL)

	response.OK(c, "Token refreshed", gin.H{
		"user":            buildUserResponse(user, role, plan),
		"accessExpiresAt": accessExpiresAt.UnixMilli(),
	})
}

// findRefreshSession resolves the session a presented refresh-token hash
// belongs to: first by the current hash, then by the superseded (prev) hash.
// Returns ok=false when it has already written a response (DB failure).
func findRefreshSession(c *gin.Context, refreshHash, userID string) (session *models.UserSession, activeMatch, prevMatch, ok bool) {
	session, err := models.FindSessionByRefreshHash(models.DB, refreshHash)
	switch {
	case err == nil:
		return session, true, false, true
	case !errors.Is(err, gorm.ErrRecordNotFound):
		logger.LogWarn(c.Request.Context(), "refresh.find_session", err, "userId", userID)
		respondRefreshInvalid(c)
		return nil, false, false, false
	}

	session, err = models.FindSessionByPrevRefreshHash(models.DB, refreshHash)
	switch {
	case err == nil:
		return session, false, true, true
	case !errors.Is(err, gorm.ErrRecordNotFound):
		logger.LogWarn(c.Request.Context(), "refresh.find_session_prev", err, "userId", userID)
		respondRefreshInvalid(c)
		return nil, false, false, false
	}

	return nil, false, false, true
}

// handleRefreshReuse responds to a detected refresh-token replay: the session
// is soft-revoked, its access token denylisted so peer services reject it
// immediately, the plan cache dropped, and the caller's cookies cleared. The
// response is the same generic 401 as every other failure (no oracle).
func (ae *AuthEndpoints) handleRefreshReuse(c *gin.Context, session *models.UserSession) {
	ctx := c.Request.Context()

	slog.Warn("refresh.token_reuse_detected",
		"userId", session.UserID, "sessionId", session.ID)

	if err := models.MarkSessionRevoked(models.DB, session.ID); err != nil {
		logger.LogWarn(ctx, "refresh.revoke_session", err, "sessionId", session.ID)
	}

	if ae.Denylist != nil {
		ttl := time.Until(session.AccessExpiresAt)
		if err := ae.Denylist.DenyTokenHash(ctx, session.AccessTokenHash, ttl); err != nil {
			logger.LogWarn(ctx, "refresh.denylist_access_token", err, "sessionId", session.ID)
		}
	}

	ae.deletePlanCache(ctx, session.UserID.String())

	publishAnalyticsEvent(ctx, "user.refresh_reuse", session.UserID.String(), "")

	clearAccessTokenCookie(c)
	clearRefreshTokenCookie(c)
	respondRefreshInvalid(c)
}

// respondRefreshInvalid is the single failure response for /auth/refresh: one
// code, one message, regardless of cause.
func respondRefreshInvalid(c *gin.Context) {
	response.Unauthorized(c, "INVALID_REFRESH_TOKEN", "Your session has expired. Please log in again.")
}

// Me returns the authenticated user's profile under the "user" key.
func (ae *AuthEndpoints) Me(c *gin.Context) {
	user, authCtx, ok := loadUserFromAuth(c)
	if !ok {
		return
	}

	plan, ok := lookupPlan(c.Request.Context(), user.PlanName)
	if !ok {
		plan = models.SubscriptionPlan{Name: user.PlanName}
	}

	response.OK(c, "Profile loaded", gin.H{
		"user": buildUserResponse(user, authCtx.Role, plan),
	})
}

// Profile returns the authenticated user's profile under the "profile" key.
func (ae *AuthEndpoints) Profile(c *gin.Context) {
	user, authCtx, ok := loadUserFromAuth(c)
	if !ok {
		return
	}

	plan, ok := lookupPlan(c.Request.Context(), user.PlanName)
	if !ok {
		plan = models.SubscriptionPlan{Name: user.PlanName}
	}

	response.OK(c, "Profile loaded", gin.H{
		"profile": buildUserResponse(user, authCtx.Role, plan),
	})
}

// Logout revokes the current session: it denylists the access token for
// cross-service invalidation, drops the user's plan cache, and clears the auth
// cookies.
func (ae *AuthEndpoints) Logout(c *gin.Context) {
	authCtx, ok := authverify.GetGinAuth(c)
	if !ok || strings.TrimSpace(authCtx.UserID) == "" {
		response.Unauthorized(c, response.CodeUnauthorized, "Your session has expired. Please log in again.")
		return
	}

	ctx := c.Request.Context()

	accessToken, hasToken := extractAccessToken(c)
	if hasToken && strings.TrimSpace(accessToken) != "" {
		if err := ae.denyAccessToken(ctx, accessToken); err != nil {
			slog.Warn("failed to deny access token", "error", err)
		}
	}

	ae.deletePlanCache(ctx, authCtx.UserID)

	clearAccessTokenCookie(c)
	clearRefreshTokenCookie(c)
	response.NoContent(c)
}

// respondWithTokens issues an access/refresh token pair for the user, persists
// the session, populates the gateway's plan cache, and sets the auth cookies.
// Shared by the signup and login success paths.
func (ae *AuthEndpoints) respondWithTokens(c *gin.Context, user models.User) {
	plan, ok := lookupPlan(c.Request.Context(), user.PlanName)
	if !ok {
		plan = models.SubscriptionPlan{Name: "free", MaxFileSizeMB: 25, MaxFilesPerJob: 10, RetentionDays: 7}
	}

	role := user.Role
	if role == "" {
		role = "user"
	}

	accessTTL := config.GetEnvDuration("JWT_ACCESS_TTL", 8*time.Hour)
	refreshTTL := config.GetEnvDuration("JWT_REFRESH_TTL", 7*24*time.Hour)

	// The gateway reads this cache on every request to enforce plan limits.
	ae.cachePlanInfo(c.Request.Context(), user.ID.String(), plan, accessTTL)

	accessToken, jti, accessExpiresAt, err := ae.Issuer.IssueAccessToken(user.ID.String(), role, nil, accessTTL)
	if err != nil {
		response.InternalErrorf(c, response.CodeServerError, "Login failed. Please try again.", err,
			"op", "issue_access_token.login", "userId", user.ID)
		return
	}

	refreshToken, _, refreshExpiresAt, err := ae.Issuer.IssueRefreshToken(user.ID.String(), refreshTTL)
	if err != nil {
		response.InternalErrorf(c, response.CodeServerError, "Login failed. Please try again.", err,
			"op", "issue_refresh_token.login", "userId", user.ID)
		return
	}

	sessionID, err := uuid.Parse(jti)
	if err != nil {
		response.InternalErrorf(c, response.CodeServerError, "Login failed. Please try again.", err,
			"op", "parse_session_jti", "jti", jti, "userId", user.ID)
		return
	}

	if err := models.StoreSession(models.DB, sessionID, user.ID, accessToken, accessExpiresAt, refreshToken, refreshExpiresAt); err != nil {
		slog.Warn("failed to store session in database", "error", err, "userId", user.ID)
	}

	setAccessTokenCookie(c, accessToken, accessTTL)
	setRefreshTokenCookie(c, refreshToken, refreshTTL)

	response.OK(c, "Welcome back!", gin.H{
		"user":            buildUserResponse(user, role, plan),
		"accessExpiresAt": accessExpiresAt.UnixMilli(),
	})
}

// denyAccessToken revokes a token on two tiers: it deletes the local session
// row and adds the token to the Redis denylist so peer services reject it
// immediately, without waiting for natural expiry. The denylist TTL matches the
// token's remaining lifetime so entries self-expire.
func (ae *AuthEndpoints) denyAccessToken(ctx context.Context, tokenStr string) error {
	hash := models.HashToken(tokenStr)
	if err := models.RevokeSessionByAccessHash(models.DB, hash); err != nil {
		slog.Warn("failed to revoke session from database", "error", err)
	}

	if ae.Denylist == nil {
		return nil
	}

	ttl, err := getTokenRemainingTTL(tokenStr)
	if err != nil {
		slog.Warn("could not parse token expiration, using default TTL", "error", err)
		ttl = config.GetEnvDuration("JWT_ACCESS_TTL", 8*time.Hour)
	}

	return ae.Denylist.DenyToken(ctx, tokenStr, ttl)
}

func buildUserResponse(user models.User, role string, plan models.SubscriptionPlan) authUser {
	if strings.TrimSpace(user.Role) != "" {
		role = user.Role
	}
	if strings.TrimSpace(role) == "" {
		role = "user"
	}
	planName := plan.Name
	if strings.TrimSpace(planName) == "" {
		planName = user.PlanName
	}
	return authUser{
		ID:       user.ID.String(),
		Email:    user.Email,
		FullName: user.FullName,
		Phone:    user.Phone,
		Country:  user.Country,
		Image:    user.ImageURL,
		Role:     role,
		PlanName: planName,
	}
}

func normalizeEmail(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func loadUserFromAuth(c *gin.Context) (models.User, authverify.AuthContext, bool) {
	authCtx, ok := authverify.GetGinAuth(c)
	if !ok || strings.TrimSpace(authCtx.UserID) == "" {
		response.Unauthorized(c, response.CodeUnauthorized, "Your session has expired. Please log in again.")
		return models.User{}, authverify.AuthContext{}, false
	}

	parsedID, err := uuid.Parse(strings.TrimSpace(authCtx.UserID))
	if err != nil {
		logger.LogErr(c.Request.Context(), "load_user.parse_id", err, "userIdRaw", authCtx.UserID)
		response.Unauthorized(c, response.CodeUnauthorized, "Your session has expired. Please log in again.")
		return models.User{}, authverify.AuthContext{}, false
	}

	var user models.User
	if err := models.DB.First(&user, "id = ?", parsedID).Error; err != nil {
		logger.LogWarn(c.Request.Context(), "load_user.db_lookup", err, "userId", parsedID)
		response.Unauthorized(c, response.CodeUnauthorized, "Your session has expired. Please log in again.")
		return models.User{}, authverify.AuthContext{}, false
	}

	return user, authCtx, true
}

func isDuplicateError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "duplicate") || strings.Contains(lower, "unique")
}

func setAccessTokenCookie(c *gin.Context, tokenStr string, ttl time.Duration) {
	name := config.GetEnv("AUTH_ACCESS_COOKIE", "access_token")
	domain := strings.TrimSpace(config.GetEnv("AUTH_COOKIE_DOMAIN", ""))
	secure := config.GetEnvBool("AUTH_COOKIE_SECURE", true)
	sameSite := config.GetEnv("AUTH_COOKIE_SAMESITE", "lax")

	if !secure {
		slog.Warn("AUTH_COOKIE_SECURE is disabled - insecure for production")
	}

	c.SetSameSite(parseSameSite(sameSite))
	maxAge := int(ttl.Seconds())
	c.SetCookie(name, tokenStr, maxAge, "/", domain, secure, true)
}

func clearAccessTokenCookie(c *gin.Context) {
	name := config.GetEnv("AUTH_ACCESS_COOKIE", "access_token")
	domain := strings.TrimSpace(config.GetEnv("AUTH_COOKIE_DOMAIN", ""))
	secure := config.GetEnvBool("AUTH_COOKIE_SECURE", true)
	sameSite := config.GetEnv("AUTH_COOKIE_SAMESITE", "lax")

	c.SetSameSite(parseSameSite(sameSite))
	c.SetCookie(name, "", -1, "/", domain, secure, true)
}

func setRefreshTokenCookie(c *gin.Context, tokenStr string, ttl time.Duration) {
	name := config.GetEnv("AUTH_REFRESH_COOKIE", "refresh_token")
	domain := strings.TrimSpace(config.GetEnv("AUTH_COOKIE_DOMAIN", ""))
	secure := config.GetEnvBool("AUTH_COOKIE_SECURE", true)
	sameSite := config.GetEnv("AUTH_COOKIE_SAMESITE", "lax")

	c.SetSameSite(parseSameSite(sameSite))
	maxAge := int(ttl.Seconds())
	c.SetCookie(name, tokenStr, maxAge, "/auth", domain, secure, true)
}

func clearRefreshTokenCookie(c *gin.Context) {
	name := config.GetEnv("AUTH_REFRESH_COOKIE", "refresh_token")
	domain := strings.TrimSpace(config.GetEnv("AUTH_COOKIE_DOMAIN", ""))
	secure := config.GetEnvBool("AUTH_COOKIE_SECURE", true)
	sameSite := config.GetEnv("AUTH_COOKIE_SAMESITE", "lax")

	c.SetSameSite(parseSameSite(sameSite))
	c.SetCookie(name, "", -1, "/auth", domain, secure, true)
}

func parseSameSite(value string) http.SameSite {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "strict":
		return http.SameSiteStrictMode
	case "none":
		return http.SameSiteNoneMode
	default:
		return http.SameSiteLaxMode
	}
}

func extractAccessToken(c *gin.Context) (string, bool) {
	authHeader := c.GetHeader("Authorization")
	if authHeader != "" {
		parts := strings.Fields(authHeader)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			token := strings.TrimSpace(parts[1])
			if token != "" {
				return token, true
			}
		}
	}

	if ctxToken, exists := c.Get("access_token"); exists {
		if token, ok := ctxToken.(string); ok && token != "" {
			return token, true
		}
	}

	// The SPA authenticates via an HttpOnly cookie, not a bearer header, so the
	// cookie is the primary source on the logout path. Without this read, logout
	// never reaches denyAccessToken and the token is never revoked.
	if cookie, err := c.Cookie(config.GetEnv("AUTH_ACCESS_COOKIE", "access_token")); err == nil {
		if token := strings.TrimSpace(cookie); token != "" {
			return token, true
		}
	}

	return "", false
}

func getTokenRemainingTTL(tokenString string) (time.Duration, error) {
	parsed, _, err := new(jwt.Parser).ParseUnverified(tokenString, &token.Claims{})
	if err != nil {
		return 0, err
	}

	claims, ok := parsed.Claims.(*token.Claims)
	if !ok || claims.ExpiresAt == nil {
		return 0, fmt.Errorf("invalid claims or missing expiration")
	}

	remaining := time.Until(claims.ExpiresAt.Time)
	if remaining < 0 {
		return 0, nil
	}

	return remaining, nil
}

// ChangePlan moves the authenticated user to a different subscription plan. It
// rejects unknown plans and no-op changes, updates the plan cache immediately so
// new limits take effect without waiting for token refresh, and emits a
// plan.changed analytics event.
// planChangeAllowedRoles are the caller roles permitted to change a plan through
// this endpoint. End users must NOT be able to self-upgrade: there is no billing
// or entitlement gate here, so an unrestricted PUT /auth/plan lets anyone grant
// themselves a paid tier. Plan changes are therefore an admin action (or, in
// future, a billing-webhook-driven internal endpoint).
var planChangeAllowedRoles = map[string]bool{
	"admin":       true,
	"super-admin": true,
}

// canChangePlan reports whether a caller with the given role may change a plan
// through PUT /auth/plan. Only admins/super-admins qualify — end users must not be
// able to self-upgrade to a paid tier since there is no billing/entitlement gate.
func canChangePlan(role string) bool {
	return planChangeAllowedRoles[strings.TrimSpace(role)]
}

func (ae *AuthEndpoints) ChangePlan(c *gin.Context) {
	user, _, ok := loadUserFromAuth(c)
	if !ok {
		return
	}

	// Authorization: only admins/super-admins may change a plan. Without this,
	// any authenticated user can upgrade themselves to a paid plan for free.
	if !canChangePlan(user.Role) {
		response.Forbidden(c, response.CodeForbidden, "You are not allowed to change plans.")
		return
	}

	var body struct {
		PlanName string `json:"planName" binding:"required,max=64"`
	}
	if !response.BindJSON(c, &body) {
		return
	}

	planName := strings.TrimSpace(body.PlanName)
	if planName == "" {
		response.BadRequest(c, response.CodeInvalidInput, "Please provide a valid plan name.")
		return
	}

	var plan models.SubscriptionPlan
	if err := models.DB.Where("name = ?", planName).First(&plan).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.BadRequest(c, "INVALID_PLAN", "The selected plan does not exist.")
			return
		}
		response.InternalErrorf(c, response.CodeServerError, "Could not update your plan. Please try again.", err,
			"op", "db.subscription_plans.lookup", "planName", planName, "userId", user.ID)
		return
	}

	oldPlan := user.PlanName
	if oldPlan == planName {
		response.BadRequest(c, "SAME_PLAN", "You are already on this plan.")
		return
	}

	if err := models.DB.Model(&user).Update("plan_name", planName).Error; err != nil {
		response.InternalErrorf(c, response.CodeServerError, "Could not update your plan. Please try again.", err,
			"op", "db.users.update_plan", "userId", user.ID, "newPlan", planName, "oldPlan", oldPlan)
		return
	}

	accessTTL := config.GetEnvDuration("JWT_ACCESS_TTL", 8*time.Hour)
	ae.cachePlanInfo(c.Request.Context(), user.ID.String(), plan, accessTTL)

	publishPlanChangedEvent(c.Request.Context(), user.ID.String(), oldPlan, planName)

	user.PlanName = planName
	response.OK(c, "Plan updated successfully", gin.H{
		"user": buildUserResponse(user, user.Role, plan),
	})
}

const planCacheKeyPrefix = "user:plan:"

type planCacheEntry struct {
	Plan      string `json:"plan"`
	MaxFileMB int    `json:"max_file_mb"`
	MaxFiles  int    `json:"max_files"`
}

func (ae *AuthEndpoints) cachePlanInfo(ctx context.Context, userID string, plan models.SubscriptionPlan, ttl time.Duration) {
	if ae.RedisClient == nil {
		return
	}
	entry := planCacheEntry{
		Plan:      plan.Name,
		MaxFileMB: plan.MaxFileSizeMB,
		MaxFiles:  plan.MaxFilesPerJob,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		slog.Warn("failed to marshal plan cache entry", "error", err)
		return
	}
	if err := ae.RedisClient.Set(ctx, planCacheKeyPrefix+userID, data, ttl).Err(); err != nil {
		slog.Warn("failed to cache plan info in Redis", "error", err, "userID", userID)
	}
}

func (ae *AuthEndpoints) deletePlanCache(ctx context.Context, userID string) {
	if ae.RedisClient == nil {
		return
	}
	if err := ae.RedisClient.Del(ctx, planCacheKeyPrefix+userID).Err(); err != nil {
		slog.Warn("failed to delete plan cache from Redis", "error", err, "userID", userID)
	}
}

func publishPlanChangedEvent(ctx context.Context, userID, oldPlan, newPlan string) {
	if natsconn.JS == nil {
		return
	}
	metadata, _ := json.Marshal(map[string]string{
		"oldPlan": oldPlan,
		"newPlan": newPlan,
	})
	event := queue.AnalyticsEvent{
		EventType: "plan.changed",
		UserID:    userID,
		PlanName:  newPlan,
		Metadata:  metadata,
		Timestamp: time.Now().UTC(),
	}
	if err := queue.PublishAnalyticsEvent(ctx, natsconn.JS, event); err != nil {
		slog.Warn("failed to publish plan.changed event", "error", err)
	}
}

func publishAnalyticsEvent(ctx context.Context, eventType string, userID string, planName string) {
	if natsconn.JS == nil {
		return
	}
	event := queue.AnalyticsEvent{
		EventType: eventType,
		UserID:    userID,
		PlanName:  planName,
		Timestamp: time.Now().UTC(),
	}
	if err := queue.PublishAnalyticsEvent(ctx, natsconn.JS, event); err != nil {
		slog.Warn("failed to publish analytics event", "eventType", eventType, "error", err)
	}
}

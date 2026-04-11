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

	"auth-service/internal/authverify"
	"auth-service/internal/models"
	"auth-service/internal/token"

	"esydocs/shared/config"
	"esydocs/shared/natsconn"
	"esydocs/shared/queue"
	"esydocs/shared/response"
)

type authCredentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	FullName string `json:"fullName"`
	Phone    string `json:"phone"`
	Country  string `json:"country"`
	Image    string `json:"image"`
}

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

type AuthEndpoints struct {
	Issuer      *token.Issuer
	Denylist    authverify.TokenDenylist
	RedisClient *redis.Client
}

func (ae *AuthEndpoints) Signup(c *gin.Context) {
	payload, ok := parseAuthPayload(c)
	if !ok {
		return
	}

	email := normalizeEmail(payload.Email)
	fullName := strings.TrimSpace(payload.FullName)
	country := strings.TrimSpace(payload.Country)
	phone := strings.TrimSpace(payload.Phone)
	image := strings.TrimSpace(payload.Image)

	if email == "" || strings.TrimSpace(payload.Password) == "" || fullName == "" || country == "" {
		response.Err(c, http.StatusBadRequest, "INVALID_INPUT", "Please fill in all required fields.")
		return
	}

	if len(payload.Password) < 8 {
		response.Err(c, http.StatusBadRequest, "WEAK_PASSWORD", "Password must be at least 8 characters")
		return
	}
	if len(payload.Password) > 128 {
		response.Err(c, http.StatusBadRequest, "INVALID_INPUT", "Password must not exceed 128 characters")
		return
	}

	var existing models.User
	if err := models.DB.Where("email = ?", email).First(&existing).Error; err == nil {
		response.Err(c, http.StatusConflict, "USER_ALREADY_EXISTS", "An account with this email already exists. Please log in instead.")
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		response.InternalError(c, "SERVER_ERROR", "Could not create your account. Please try again.")
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(payload.Password), bcrypt.DefaultCost)
	if err != nil {
		response.InternalError(c, "SERVER_ERROR", "Could not create your account. Please try again.")
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
		response.InternalError(c, "SERVER_ERROR", "Could not create your account. Please try again.")
		return
	}

	publishAnalyticsEvent(c.Request.Context(), "user.signup", user.ID.String(), user.PlanName)
	ae.respondWithTokens(c, user)
}

func (ae *AuthEndpoints) Login(c *gin.Context) {
	payload, ok := parseAuthPayload(c)
	if !ok {
		return
	}

	email := normalizeEmail(payload.Email)
	if email == "" || strings.TrimSpace(payload.Password) == "" {
		response.Unauthorized(c, "INVALID_CREDENTIALS", "Incorrect email or password. Please try again.")
		return
	}

	if len(payload.Password) > 128 {
		response.Unauthorized(c, "INVALID_CREDENTIALS", "Incorrect email or password. Please try again.")
		return
	}

	var user models.User
	if err := models.DB.Where("email = ?", email).First(&user).Error; err != nil {
		response.Unauthorized(c, "INVALID_CREDENTIALS", "Incorrect email or password. Please try again.")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(payload.Password)); err != nil {
		response.Unauthorized(c, "INVALID_CREDENTIALS", "Incorrect email or password. Please try again.")
		return
	}

	publishAnalyticsEvent(c.Request.Context(), "user.login", user.ID.String(), user.PlanName)
	ae.respondWithTokens(c, user)
}

func (ae *AuthEndpoints) Refresh(c *gin.Context) {
	cookieName := config.GetEnv("AUTH_REFRESH_COOKIE", "refresh_token")
	refreshToken, err := c.Cookie(cookieName)
	if err != nil || strings.TrimSpace(refreshToken) == "" {
		response.Unauthorized(c, "INVALID_REFRESH_TOKEN", "Your session has expired. Please log in again.")
		return
	}

	userID, err := ae.Issuer.VerifyRefreshToken(refreshToken)
	if err != nil {
		response.Unauthorized(c, "INVALID_REFRESH_TOKEN", "Your session has expired. Please log in again.")
		return
	}

	session, err := models.FindSessionByRefreshHash(models.DB, models.HashToken(refreshToken))
	if err != nil {
		response.Unauthorized(c, "INVALID_REFRESH_TOKEN", "Your session has expired. Please log in again.")
		return
	}

	parsedID, err := uuid.Parse(strings.TrimSpace(userID))
	if err != nil {
		response.Unauthorized(c, "INVALID_REFRESH_TOKEN", "Your session has expired. Please log in again.")
		return
	}

	var user models.User
	if err := models.DB.First(&user, "id = ?", parsedID).Error; err != nil {
		response.Unauthorized(c, "INVALID_REFRESH_TOKEN", "Your session has expired. Please log in again.")
		return
	}

	var plan models.SubscriptionPlan
	if err := models.DB.Where("name = ?", user.PlanName).First(&plan).Error; err != nil {
		plan = models.SubscriptionPlan{Name: "free", MaxFileSizeMB: 25, MaxFilesPerJob: 10, RetentionDays: 7}
	}

	role := user.Role
	if role == "" {
		role = "user"
	}

	accessTTL := config.GetEnvDuration("JWT_ACCESS_TTL", 8*time.Hour)

	// Refresh plan cache in Redis
	ae.cachePlanInfo(c.Request.Context(), user.ID.String(), plan, accessTTL)

	accessToken, _, accessExpiresAt, err := ae.Issuer.IssueAccessToken(user.ID.String(), role, nil, accessTTL)
	if err != nil {
		response.InternalError(c, "SERVER_ERROR", "Failed to refresh token. Please try again.")
		return
	}

	session.AccessTokenHash = models.HashToken(accessToken)
	session.AccessExpiresAt = accessExpiresAt
	if err := models.DB.Save(session).Error; err != nil {
		slog.Warn("failed to update session in database", "error", err)
	}

	setAccessTokenCookie(c, accessToken, accessTTL)

	response.OK(c, "Token refreshed", gin.H{
		"user":            buildUserResponse(user, role, plan),
		"accessExpiresAt": accessExpiresAt.UnixMilli(),
	})
}

func (ae *AuthEndpoints) Me(c *gin.Context) {
	user, authCtx, ok := loadUserFromAuth(c)
	if !ok {
		return
	}

	var plan models.SubscriptionPlan
	if err := models.DB.Where("name = ?", user.PlanName).First(&plan).Error; err != nil {
		plan = models.SubscriptionPlan{Name: user.PlanName}
	}

	response.OK(c, "Profile loaded", gin.H{
		"user": buildUserResponse(user, authCtx.Role, plan),
	})
}

func (ae *AuthEndpoints) Profile(c *gin.Context) {
	user, authCtx, ok := loadUserFromAuth(c)
	if !ok {
		return
	}

	var plan models.SubscriptionPlan
	if err := models.DB.Where("name = ?", user.PlanName).First(&plan).Error; err != nil {
		plan = models.SubscriptionPlan{Name: user.PlanName}
	}

	response.OK(c, "Profile loaded", gin.H{
		"profile": buildUserResponse(user, authCtx.Role, plan),
	})
}

func (ae *AuthEndpoints) Logout(c *gin.Context) {
	authCtx, ok := authverify.GetGinAuth(c)
	if !ok || strings.TrimSpace(authCtx.UserID) == "" {
		response.Unauthorized(c, "UNAUTHORIZED", "Your session has expired. Please log in again.")
		return
	}

	ctx := c.Request.Context()

	accessToken, hasToken := extractAccessToken(c)
	if hasToken && strings.TrimSpace(accessToken) != "" {
		if err := ae.denyAccessToken(ctx, accessToken); err != nil {
			slog.Warn("failed to deny access token", "error", err)
		}
	}

	// Delete plan cache from Redis
	ae.deletePlanCache(ctx, authCtx.UserID)

	clearAccessTokenCookie(c)
	clearRefreshTokenCookie(c)
	response.NoContent(c)
}

func (ae *AuthEndpoints) respondWithTokens(c *gin.Context, user models.User) {
	var plan models.SubscriptionPlan
	if err := models.DB.Where("name = ?", user.PlanName).First(&plan).Error; err != nil {
		plan = models.SubscriptionPlan{Name: "free", MaxFileSizeMB: 25, MaxFilesPerJob: 10, RetentionDays: 7}
	}

	role := user.Role
	if role == "" {
		role = "user"
	}

	accessTTL := config.GetEnvDuration("JWT_ACCESS_TTL", 8*time.Hour)
	refreshTTL := config.GetEnvDuration("JWT_REFRESH_TTL", 7*24*time.Hour)

	// Cache plan info in Redis for the API gateway to read
	ae.cachePlanInfo(c.Request.Context(), user.ID.String(), plan, accessTTL)

	accessToken, jti, accessExpiresAt, err := ae.Issuer.IssueAccessToken(user.ID.String(), role, nil, accessTTL)
	if err != nil {
		response.InternalError(c, "SERVER_ERROR", "Login failed. Please try again.")
		return
	}

	refreshToken, _, refreshExpiresAt, err := ae.Issuer.IssueRefreshToken(user.ID.String(), refreshTTL)
	if err != nil {
		response.InternalError(c, "SERVER_ERROR", "Login failed. Please try again.")
		return
	}

	sessionID, err := uuid.Parse(jti)
	if err != nil {
		response.InternalError(c, "SERVER_ERROR", "Login failed. Please try again.")
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

func (ae *AuthEndpoints) denyAccessToken(ctx context.Context, tokenStr string) error {
	// Delete the session from the database
	hash := models.HashToken(tokenStr)
	if err := models.RevokeSessionByAccessHash(models.DB, hash); err != nil {
		slog.Warn("failed to revoke session from database", "error", err)
	}

	// Also add to Redis denylist for cross-service invalidation
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

func parseAuthPayload(c *gin.Context) (authCredentials, bool) {
	var payload authCredentials
	if err := c.ShouldBindJSON(&payload); err != nil {
		response.BadRequest(c, "INVALID_INPUT", "Invalid request. Please try again.")
		return authCredentials{}, false
	}
	return payload, true
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
		response.Unauthorized(c, "UNAUTHORIZED", "Your session has expired. Please log in again.")
		return models.User{}, authverify.AuthContext{}, false
	}

	parsedID, err := uuid.Parse(strings.TrimSpace(authCtx.UserID))
	if err != nil {
		response.Unauthorized(c, "UNAUTHORIZED", "Your session has expired. Please log in again.")
		return models.User{}, authverify.AuthContext{}, false
	}

	var user models.User
	if err := models.DB.First(&user, "id = ?", parsedID).Error; err != nil {
		response.Unauthorized(c, "UNAUTHORIZED", "Your session has expired. Please log in again.")
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

func (ae *AuthEndpoints) ChangePlan(c *gin.Context) {
	user, _, ok := loadUserFromAuth(c)
	if !ok {
		return
	}

	var body struct {
		PlanName string `json:"planName" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, "INVALID_INPUT", "Please provide a valid plan name.")
		return
	}

	planName := strings.TrimSpace(body.PlanName)
	if planName == "" {
		response.BadRequest(c, "INVALID_INPUT", "Please provide a valid plan name.")
		return
	}

	var plan models.SubscriptionPlan
	if err := models.DB.Where("name = ?", planName).First(&plan).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.BadRequest(c, "INVALID_PLAN", "The selected plan does not exist.")
			return
		}
		response.InternalError(c, "SERVER_ERROR", "Could not update your plan. Please try again.")
		return
	}

	oldPlan := user.PlanName
	if oldPlan == planName {
		response.BadRequest(c, "SAME_PLAN", "You are already on this plan.")
		return
	}

	if err := models.DB.Model(&user).Update("plan_name", planName).Error; err != nil {
		response.InternalError(c, "SERVER_ERROR", "Could not update your plan. Please try again.")
		return
	}

	// Update plan cache in Redis immediately
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
	Plan       string `json:"plan"`
	MaxFileMB  int    `json:"max_file_mb"`
	MaxFiles   int    `json:"max_files"`
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

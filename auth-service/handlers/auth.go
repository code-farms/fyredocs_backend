package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"auth-service/internal/authverify"
	"auth-service/internal/models"
	"auth-service/internal/token"

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
	Issuer   *token.Issuer
	Denylist authverify.TokenDenylist
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
		response.Err(c, http.StatusBadRequest, "INVALID_INPUT", "Email, password, full name, and country are required")
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
		response.Err(c, http.StatusConflict, "USER_ALREADY_EXISTS", "User already exists")
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		response.InternalError(c, "SERVER_ERROR", "Unable to create user")
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(payload.Password), bcrypt.DefaultCost)
	if err != nil {
		response.InternalError(c, "SERVER_ERROR", "Unable to create user")
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
			response.Err(c, http.StatusConflict, "USER_ALREADY_EXISTS", "User already exists")
			return
		}
		response.InternalError(c, "SERVER_ERROR", "Unable to create user")
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
		response.Unauthorized(c, "INVALID_CREDENTIALS", "Invalid credentials")
		return
	}

	if len(payload.Password) > 128 {
		response.Unauthorized(c, "INVALID_CREDENTIALS", "Invalid credentials")
		return
	}

	var user models.User
	if err := models.DB.Where("email = ?", email).First(&user).Error; err != nil {
		response.Unauthorized(c, "INVALID_CREDENTIALS", "Invalid credentials")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(payload.Password)); err != nil {
		response.Unauthorized(c, "INVALID_CREDENTIALS", "Invalid credentials")
		return
	}

	publishAnalyticsEvent(c.Request.Context(), "user.login", user.ID.String(), user.PlanName)
	ae.respondWithTokens(c, user)
}

func (ae *AuthEndpoints) Refresh(c *gin.Context) {
	response.Err(c, http.StatusGone, "ENDPOINT_DEPRECATED", "Refresh tokens are no longer supported. Please login again to get a new access token.")
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

	response.OK(c, "User profile retrieved", gin.H{
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

	response.OK(c, "User profile retrieved", gin.H{
		"profile": buildUserResponse(user, authCtx.Role, plan),
	})
}

func (ae *AuthEndpoints) Logout(c *gin.Context) {
	authCtx, ok := authverify.GetGinAuth(c)
	if !ok || strings.TrimSpace(authCtx.UserID) == "" {
		response.Unauthorized(c, "UNAUTHORIZED", "Unauthorized")
		return
	}

	ctx := c.Request.Context()

	accessToken, hasToken := extractAccessToken(c)
	if hasToken && strings.TrimSpace(accessToken) != "" {
		if err := ae.denyAccessToken(ctx, accessToken); err != nil {
			slog.Warn("failed to deny access token", "error", err)
		}
	}

	clearAccessTokenCookie(c)
	response.NoContent(c)
}

func (ae *AuthEndpoints) respondWithTokens(c *gin.Context, user models.User) {
	var plan models.SubscriptionPlan
	if err := models.DB.Where("name = ?", user.PlanName).First(&plan).Error; err != nil {
		plan = models.SubscriptionPlan{Name: "free", MaxFileSizeMB: 25, MaxFilesPerJob: 10, RetentionDays: 7}
	}

	planInfo := token.PlanInfo{
		Name:           plan.Name,
		MaxFileSizeMB:  plan.MaxFileSizeMB,
		MaxFilesPerJob: plan.MaxFilesPerJob,
	}

	role := user.Role
	if role == "" {
		role = "user"
	}

	accessToken, err := ae.Issuer.IssueAccessToken(user.ID.String(), role, nil, planInfo)
	if err != nil {
		response.InternalError(c, "SERVER_ERROR", "Unable to issue token")
		return
	}

	setAccessTokenCookie(c, accessToken)

	response.OK(c, "Authentication successful", gin.H{
		"user": buildUserResponse(user, role, plan),
	})
}

func (ae *AuthEndpoints) denyAccessToken(ctx context.Context, tokenStr string) error {
	if ae.Denylist == nil {
		return nil
	}

	ttl, err := getTokenRemainingTTL(tokenStr)
	if err != nil {
		slog.Warn("could not parse token expiration, using default TTL", "error", err)
		ttl = 15 * time.Minute
	}

	return ae.Denylist.DenyToken(ctx, tokenStr, ttl)
}

func parseAuthPayload(c *gin.Context) (authCredentials, bool) {
	var payload authCredentials
	if err := c.ShouldBindJSON(&payload); err != nil {
		response.BadRequest(c, "INVALID_INPUT", "Invalid request")
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
		response.Unauthorized(c, "UNAUTHORIZED", "Unauthorized")
		return models.User{}, authverify.AuthContext{}, false
	}

	parsedID, err := uuid.Parse(strings.TrimSpace(authCtx.UserID))
	if err != nil {
		response.Unauthorized(c, "UNAUTHORIZED", "Unauthorized")
		return models.User{}, authverify.AuthContext{}, false
	}

	var user models.User
	if err := models.DB.First(&user, "id = ?", parsedID).Error; err != nil {
		response.Unauthorized(c, "UNAUTHORIZED", "Unauthorized")
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

func setAccessTokenCookie(c *gin.Context, tokenStr string) {
	name := getEnv("AUTH_ACCESS_COOKIE", "access_token")
	domain := strings.TrimSpace(getEnv("AUTH_COOKIE_DOMAIN", ""))
	secure := getEnvBool("AUTH_COOKIE_SECURE", true)
	sameSite := getEnv("AUTH_COOKIE_SAMESITE", "lax")
	ttl := getEnvDuration("JWT_ACCESS_TTL", 8*time.Hour)

	if !secure {
		slog.Warn("AUTH_COOKIE_SECURE is disabled - insecure for production")
	}

	c.SetSameSite(parseSameSite(sameSite))
	maxAge := int(ttl.Seconds())
	c.SetCookie(name, tokenStr, maxAge, "/", domain, secure, true)
}

func clearAccessTokenCookie(c *gin.Context) {
	name := getEnv("AUTH_ACCESS_COOKIE", "access_token")
	domain := strings.TrimSpace(getEnv("AUTH_COOKIE_DOMAIN", ""))
	secure := getEnvBool("AUTH_COOKIE_SECURE", true)
	sameSite := getEnv("AUTH_COOKIE_SAMESITE", "lax")

	c.SetSameSite(parseSameSite(sameSite))
	c.SetCookie(name, "", -1, "/", domain, secure, true)
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

func getEnv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "y":
		return true
	case "0", "false", "no", "n":
		return false
	default:
		return fallback
	}
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
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

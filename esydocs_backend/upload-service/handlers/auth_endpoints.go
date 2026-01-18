package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"upload-service/auth"
	"upload-service/database"
	"upload-service/redisstore"
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
}

const refreshTokenKeyPrefix = "auth:refresh"

func Signup(c *gin.Context) {
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
		writeAuthError(c, http.StatusBadRequest, "INVALID_INPUT", "Email, password, full name, and country are required")
		return
	}

	var existing database.User
	if err := database.DB.Where("email = ?", email).First(&existing).Error; err == nil {
		writeAuthError(c, http.StatusConflict, "USER_ALREADY_EXISTS", "User already exists")
		return
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		writeAuthError(c, http.StatusInternalServerError, "SERVER_ERROR", "Unable to create user")
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(payload.Password), bcrypt.DefaultCost)
	if err != nil {
		writeAuthError(c, http.StatusInternalServerError, "SERVER_ERROR", "Unable to create user")
		return
	}

	user := database.User{
		Email:        email,
		FullName:     fullName,
		Phone:        phone,
		Country:      country,
		ImageURL:     image,
		PasswordHash: string(passwordHash),
	}
	if err := database.DB.Create(&user).Error; err != nil {
		if isDuplicateError(err) {
			writeAuthError(c, http.StatusConflict, "USER_ALREADY_EXISTS", "User already exists")
			return
		}
		writeAuthError(c, http.StatusInternalServerError, "SERVER_ERROR", "Unable to create user")
		return
	}

	respondWithTokens(c, user)
}

func Login(c *gin.Context) {
	payload, ok := parseAuthPayload(c)
	if !ok {
		return
	}

	email := normalizeEmail(payload.Email)
	if email == "" || strings.TrimSpace(payload.Password) == "" {
		writeAuthError(c, http.StatusUnauthorized, "INVALID_CREDENTIALS", "Invalid credentials")
		return
	}

	var user database.User
	if err := database.DB.Where("email = ?", email).First(&user).Error; err != nil {
		writeAuthError(c, http.StatusUnauthorized, "INVALID_CREDENTIALS", "Invalid credentials")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(payload.Password)); err != nil {
		writeAuthError(c, http.StatusUnauthorized, "INVALID_CREDENTIALS", "Invalid credentials")
		return
	}

	respondWithTokens(c, user)
}

func Refresh(c *gin.Context) {
	// Refresh endpoint is deprecated - using long-lived access tokens instead
	writeAuthError(c, http.StatusGone, "ENDPOINT_DEPRECATED", "Refresh tokens are no longer supported. Please login again to get a new access token.")
}

func Me(c *gin.Context) {
	user, authCtx, ok := loadUserFromAuth(c)
	if !ok {
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"user": buildUserResponse(user, authCtx.Role),
	})
}

func Profile(c *gin.Context) {
	user, authCtx, ok := loadUserFromAuth(c)
	if !ok {
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"profile": buildUserResponse(user, authCtx.Role),
	})
}

func Logout(c *gin.Context) {
	authCtx, ok := auth.GetGinAuth(c)
	if !ok || strings.TrimSpace(authCtx.UserID) == "" {
		writeAuthError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
		return
	}

	ctx := c.Request.Context()

	// Revoke access token by adding to denylist
	accessToken, hasToken := extractAccessToken(c)
	if hasToken && strings.TrimSpace(accessToken) != "" {
		if err := denyAccessToken(ctx, accessToken); err != nil {
			log.Printf("WARNING: Failed to deny access token: %v", err)
			// Continue with logout even if denylist fails (fail gracefully)
		}
	}

	// Clear access token cookie
	clearAccessTokenCookie(c)

	c.Status(http.StatusNoContent)
}

func parseAuthPayload(c *gin.Context) (authCredentials, bool) {
	var payload authCredentials
	if err := c.ShouldBindJSON(&payload); err != nil {
		writeAuthError(c, http.StatusBadRequest, "INVALID_INPUT", "Invalid request")
		return authCredentials{}, false
	}
	return payload, true
}

func respondWithTokens(c *gin.Context, user database.User) {
	issuer, err := auth.NewIssuerFromEnv()
	if err != nil {
		writeAuthError(c, http.StatusInternalServerError, "SERVER_ERROR", "Unable to issue token")
		return
	}

	accessToken, err := issuer.IssueAccessToken(user.ID.String(), "user", nil)
	if err != nil {
		writeAuthError(c, http.StatusInternalServerError, "SERVER_ERROR", "Unable to issue token")
		return
	}

	// Set access token as secure HTTP-only cookie
	setAccessTokenCookie(c, accessToken)

	c.JSON(http.StatusOK, gin.H{
		"user": buildUserResponse(user, "user"),
	})
}

func respondWithAccessToken(c *gin.Context, user database.User) {
	issuer, err := auth.NewIssuerFromEnv()
	if err != nil {
		writeAuthError(c, http.StatusInternalServerError, "SERVER_ERROR", "Unable to issue token")
		return
	}

	accessToken, err := issuer.IssueAccessToken(user.ID.String(), "user", nil)
	if err != nil {
		writeAuthError(c, http.StatusInternalServerError, "SERVER_ERROR", "Unable to issue token")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"accessToken": accessToken,
		"user":        buildUserResponse(user, "user"),
	})
}

func buildUserResponse(user database.User, role string) authUser {
	if strings.TrimSpace(role) == "" {
		role = "user"
	}
	return authUser{
		ID:       user.ID.String(),
		Email:    user.Email,
		FullName: user.FullName,
		Phone:    user.Phone,
		Country:  user.Country,
		Image:    user.ImageURL,
		Role:     role,
	}
}

func normalizeEmail(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func loadUserFromAuth(c *gin.Context) (database.User, auth.AuthContext, bool) {
	authCtx, ok := auth.GetGinAuth(c)
	if !ok || strings.TrimSpace(authCtx.UserID) == "" {
		writeAuthError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
		return database.User{}, auth.AuthContext{}, false
	}

	parsedID, err := uuid.Parse(strings.TrimSpace(authCtx.UserID))
	if err != nil {
		writeAuthError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
		return database.User{}, auth.AuthContext{}, false
	}

	var user database.User
	if err := database.DB.First(&user, "id = ?", parsedID).Error; err != nil {
		writeAuthError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
		return database.User{}, auth.AuthContext{}, false
	}

	return user, authCtx, true
}

func writeAuthError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{
		"code":    code,
		"message": message,
	})
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

func newRefreshToken() (string, time.Duration, error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", 0, err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	ttl := getEnvDuration("AUTH_REFRESH_TTL", 720*time.Hour)
	return token, ttl, nil
}

func storeRefreshToken(ctx context.Context, token, userID string, ttl time.Duration) error {
	if redisstore.Client == nil {
		return fmt.Errorf("redis not configured")
	}
	key := fmt.Sprintf("%s:%s", refreshTokenKeyPrefix, token)
	return redisstore.Client.Set(ctx, key, userID, ttl).Err()
}

func refreshTokenUser(ctx context.Context, token string) (string, error) {
	if redisstore.Client == nil {
		return "", fmt.Errorf("redis not configured")
	}
	key := fmt.Sprintf("%s:%s", refreshTokenKeyPrefix, token)
	return redisstore.Client.Get(ctx, key).Result()
}

func rotateRefreshToken(ctx context.Context, oldToken, userID string, c *gin.Context) {
	_ = revokeRefreshToken(ctx, oldToken)
	newToken, ttl, err := newRefreshToken()
	if err != nil {
		return
	}
	if err := storeRefreshToken(ctx, newToken, userID, ttl); err != nil {
		return
	}
	setRefreshCookie(c, newToken, ttl)
}

func revokeRefreshToken(ctx context.Context, token string) error {
	if redisstore.Client == nil {
		return fmt.Errorf("redis not configured")
	}
	key := fmt.Sprintf("%s:%s", refreshTokenKeyPrefix, token)
	return redisstore.Client.Del(ctx, key).Err()
}

func setAccessTokenCookie(c *gin.Context, token string) {
	name := getEnv("AUTH_ACCESS_COOKIE", "access_token")
	domain := strings.TrimSpace(getEnv("AUTH_COOKIE_DOMAIN", ""))
	secure := getEnvBool("AUTH_COOKIE_SECURE", true)
	sameSite := getEnv("AUTH_COOKIE_SAMESITE", "lax")
	ttl := getEnvDuration("JWT_ACCESS_TTL", 8*time.Hour)

	// SECURITY WARNING: Check if HTTPS is disabled
	if !secure {
		log.Printf("WARNING: AUTH_COOKIE_SECURE is disabled - access tokens will be sent over unencrypted HTTP connections. This is INSECURE and should only be used for local development. Set AUTH_COOKIE_SECURE=true in production.")
	}

	c.SetSameSite(parseSameSite(sameSite))
	maxAge := int(ttl.Seconds())
	c.SetCookie(name, token, maxAge, "/", domain, secure, true) // httpOnly=true
}

func setRefreshCookie(c *gin.Context, token string, ttl time.Duration) {
	name := getEnv("AUTH_REFRESH_COOKIE", "refresh_token")
	domain := strings.TrimSpace(getEnv("AUTH_COOKIE_DOMAIN", ""))
	secure := getEnvBool("AUTH_COOKIE_SECURE", true)
	sameSite := getEnv("AUTH_COOKIE_SAMESITE", "lax")

	// SECURITY WARNING: Check if HTTPS is disabled
	if !secure {
		log.Printf("WARNING: AUTH_COOKIE_SECURE is disabled - refresh tokens will be sent over unencrypted HTTP connections. This is INSECURE and should only be used for local development. Set AUTH_COOKIE_SECURE=true in production.")
	}

	c.SetSameSite(parseSameSite(sameSite))
	maxAge := int(ttl.Seconds())
	c.SetCookie(name, token, maxAge, "/", domain, secure, true)
}

func clearAccessTokenCookie(c *gin.Context) {
	name := getEnv("AUTH_ACCESS_COOKIE", "access_token")
	domain := strings.TrimSpace(getEnv("AUTH_COOKIE_DOMAIN", ""))
	secure := getEnvBool("AUTH_COOKIE_SECURE", true)
	sameSite := getEnv("AUTH_COOKIE_SAMESITE", "lax")

	c.SetSameSite(parseSameSite(sameSite))
	c.SetCookie(name, "", -1, "/", domain, secure, true)
}

func clearRefreshCookie(c *gin.Context, name string) {
	domain := strings.TrimSpace(getEnv("AUTH_COOKIE_DOMAIN", ""))
	secure := getEnvBool("AUTH_COOKIE_SECURE", true)
	sameSite := getEnv("AUTH_COOKIE_SAMESITE", "lax")

	// SECURITY WARNING: Check if HTTPS is disabled
	if !secure {
		log.Printf("WARNING: AUTH_COOKIE_SECURE is disabled - this is insecure for production use")
	}

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

// extractAccessToken extracts the access token from the Authorization header or context
func extractAccessToken(c *gin.Context) (string, bool) {
	// Try Authorization header first
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

	// Try from context (set by middleware)
	if ctxToken, exists := c.Get("access_token"); exists {
		if token, ok := ctxToken.(string); ok && token != "" {
			return token, true
		}
	}

	return "", false
}

// denyAccessToken adds the access token to the denylist
func denyAccessToken(ctx context.Context, token string) error {
	// Check if denylist is enabled
	denylistEnabled := getEnvBool("AUTH_DENYLIST_ENABLED", true)
	if !denylistEnabled {
		return nil // Silently skip if disabled
	}

	// Get denylist instance
	denylist := getDenylist()
	if denylist == nil {
		return fmt.Errorf("denylist not available")
	}

	// Parse token to get expiration time for TTL
	ttl, err := getTokenRemainingTTL(token)
	if err != nil {
		log.Printf("WARNING: Could not parse token expiration, using default TTL: %v", err)
		ttl = 15 * time.Minute // Default access token TTL
	}

	// Add token to denylist with remaining TTL
	return denylist.DenyToken(ctx, token, ttl)
}

// getDenylist returns the singleton denylist instance
var denylistInstance auth.TokenDenylist
var denylistOnce sync.Once

func getDenylist() auth.TokenDenylist {
	denylistOnce.Do(func() {
		if getEnvBool("AUTH_DENYLIST_ENABLED", true) {
			denylistInstance = auth.NewRedisTokenDenylist(redisstore.Client, os.Getenv("AUTH_DENYLIST_PREFIX"))
		}
	})
	return denylistInstance
}

// getTokenRemainingTTL calculates the remaining TTL from the token's expiration claim
func getTokenRemainingTTL(tokenString string) (time.Duration, error) {
	// Parse without verifying (we just need the expiration claim)
	token, _, err := new(jwt.Parser).ParseUnverified(tokenString, &auth.Claims{})
	if err != nil {
		return 0, err
	}

	claims, ok := token.Claims.(*auth.Claims)
	if !ok || claims.ExpiresAt == nil {
		return 0, fmt.Errorf("invalid claims or missing expiration")
	}

	remaining := time.Until(claims.ExpiresAt.Time)
	if remaining < 0 {
		return 0, nil // Token already expired
	}

	return remaining, nil
}

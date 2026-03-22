package token

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// PlanInfo carries the plan name and limits to be embedded in the JWT.
// Defined here to keep the token package free of model imports.
type PlanInfo struct {
	Name           string
	MaxFileSizeMB  int
	MaxFilesPerJob int
}

type Claims struct {
	jwt.RegisteredClaims
	Role               string   `json:"role,omitempty"`
	Scope              []string `json:"scope,omitempty"`
	Plan               string   `json:"plan,omitempty"`
	PlanMaxFileSizeMB  int      `json:"plan_max_file_mb,omitempty"`
	PlanMaxFilesPerJob int      `json:"plan_max_files,omitempty"`
	IsGuest            bool     `json:"is_guest,omitempty"`
}

type Issuer struct {
	hmacSecret []byte
	issuer     string
	audience   string
}

func NewIssuerFromEnv() (*Issuer, error) {
	secret := os.Getenv("JWT_HS256_SECRET")
	if secret == "" {
		secret = os.Getenv("JWT_SECRET")
	}
	if strings.TrimSpace(secret) == "" {
		return nil, fmt.Errorf("hs256 secret not configured")
	}

	issuer := strings.TrimSpace(os.Getenv("JWT_ISSUER"))
	if issuer == "" {
		return nil, fmt.Errorf("JWT_ISSUER environment variable is required but not set")
	}
	audience := strings.TrimSpace(os.Getenv("JWT_AUDIENCE"))
	if audience == "" {
		return nil, fmt.Errorf("JWT_AUDIENCE environment variable is required but not set")
	}

	return &Issuer{
		hmacSecret: []byte(secret),
		issuer:     issuer,
		audience:   audience,
	}, nil
}

// IssueAccessToken creates a signed JWT. The caller decides the TTL.
// Returns the signed token string, the JTI (token ID), and the expiration time.
func (i *Issuer) IssueAccessToken(userID, role string, scope []string, plan PlanInfo, ttl time.Duration) (tokenStr string, jti string, expiresAt time.Time, err error) {
	if i == nil || len(i.hmacSecret) == 0 {
		return "", "", time.Time{}, fmt.Errorf("issuer not configured")
	}
	now := time.Now()
	jti = uuid.NewString()
	expiresAt = now.Add(ttl)
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			Subject:   strings.TrimSpace(userID),
			Issuer:    i.issuer,
			Audience:  []string{i.audience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
		Role:               strings.TrimSpace(role),
		Scope:              scope,
		Plan:               plan.Name,
		PlanMaxFileSizeMB:  plan.MaxFileSizeMB,
		PlanMaxFilesPerJob: plan.MaxFilesPerJob,
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err = tok.SignedString(i.hmacSecret)
	return tokenStr, jti, expiresAt, err
}

// IssueRefreshToken creates a signed JWT with minimal claims (no role/plan).
// The caller decides the TTL. Returns the signed token string, JTI, and expiration time.
func (i *Issuer) IssueRefreshToken(userID string, ttl time.Duration) (tokenStr string, jti string, expiresAt time.Time, err error) {
	if i == nil || len(i.hmacSecret) == 0 {
		return "", "", time.Time{}, fmt.Errorf("issuer not configured")
	}
	now := time.Now()
	jti = uuid.NewString()
	expiresAt = now.Add(ttl)
	claims := jwt.RegisteredClaims{
		ID:        jti,
		Subject:   strings.TrimSpace(userID),
		Issuer:    i.issuer,
		Audience:  []string{i.audience},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(expiresAt),
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err = tok.SignedString(i.hmacSecret)
	return tokenStr, jti, expiresAt, err
}

// VerifyRefreshToken parses and validates a refresh token's signature and expiry.
// Returns the subject (userID) if valid.
func (i *Issuer) VerifyRefreshToken(tokenStr string) (userID string, err error) {
	if i == nil || len(i.hmacSecret) == 0 {
		return "", fmt.Errorf("issuer not configured")
	}

	parsed, err := jwt.ParseWithClaims(tokenStr, &jwt.RegisteredClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return i.hmacSecret, nil
	}, jwt.WithIssuer(i.issuer), jwt.WithAudience(i.audience))
	if err != nil {
		return "", err
	}

	claims, ok := parsed.Claims.(*jwt.RegisteredClaims)
	if !ok || claims.Subject == "" {
		return "", fmt.Errorf("invalid refresh token claims")
	}

	return claims.Subject, nil
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

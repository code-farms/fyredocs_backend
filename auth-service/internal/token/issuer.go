package token

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
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
	accessTTL  time.Duration
}

func NewIssuerFromEnv() (*Issuer, error) {
	secret := os.Getenv("JWT_HS256_SECRET")
	if secret == "" {
		secret = os.Getenv("JWT_SECRET")
	}
	if strings.TrimSpace(secret) == "" {
		return nil, fmt.Errorf("hs256 secret not configured")
	}

	return &Issuer{
		hmacSecret: []byte(secret),
		issuer:     strings.TrimSpace(os.Getenv("JWT_ISSUER")),
		audience:   strings.TrimSpace(os.Getenv("JWT_AUDIENCE")),
		accessTTL:  getEnvDuration("JWT_ACCESS_TTL", 8*time.Hour),
	}, nil
}

func (i *Issuer) IssueAccessToken(userID, role string, scope []string, plan PlanInfo) (string, error) {
	if i == nil || len(i.hmacSecret) == 0 {
		return "", fmt.Errorf("issuer not configured")
	}
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strings.TrimSpace(userID),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(i.accessTTL)),
		},
		Role:               strings.TrimSpace(role),
		Scope:              scope,
		Plan:               plan.Name,
		PlanMaxFileSizeMB:  plan.MaxFileSizeMB,
		PlanMaxFilesPerJob: plan.MaxFilesPerJob,
	}
	if i.issuer != "" {
		claims.Issuer = i.issuer
	}
	if i.audience != "" {
		claims.Audience = []string{i.audience}
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(i.hmacSecret)
}

func (i *Issuer) AccessTTL() time.Duration {
	return i.accessTTL
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

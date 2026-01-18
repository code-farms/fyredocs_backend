package auth

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Issuer struct {
	hmacSecret []byte
	issuer     string
	audience   string
	accessTTL  time.Duration
}

func NewIssuerFromEnv() (*Issuer, error) {
	allowed := parseCommaList(getEnv("JWT_ALLOWED_ALGS", "HS256"))
	allowedHS256 := false
	for _, alg := range allowed {
		if strings.EqualFold(strings.TrimSpace(alg), jwt.SigningMethodHS256.Alg()) {
			allowedHS256 = true
			break
		}
	}
	if !allowedHS256 {
		return nil, fmt.Errorf("hs256 not enabled for jwt issuance")
	}

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
		accessTTL:  getEnvDuration("JWT_ACCESS_TTL", 15*time.Minute),
	}, nil
}

func (i *Issuer) IssueAccessToken(userID, role string, scope []string) (string, error) {
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
		Role:  strings.TrimSpace(role),
		Scope: scope,
	}
	if i.issuer != "" {
		claims.Issuer = i.issuer
	}
	if i.audience != "" {
		claims.Audience = []string{i.audience}
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(i.hmacSecret)
}

// Package authverify verifies the platform's JWTs and derives the per-request
// AuthContext used for authorization. It is the read side of authentication:
// auth-service issues tokens, and every service (via the gateway or directly)
// uses this package to verify them, resolve identity/role/scope, honor the
// revocation denylist, and enforce role/scope requirements.
package authverify

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"fyredocs/shared/config"
	"github.com/golang-jwt/jwt/v5"
)

// Verification error sentinels returned by Verify, letting callers distinguish a
// missing, malformed, or expired token.
var (
	ErrTokenMissing = errors.New("token missing")
	ErrTokenInvalid = errors.New("token invalid")
	ErrTokenExpired = errors.New("token expired")
)

// VerifierConfig configures a Verifier: the permitted signing algorithms and
// their keys, the expected issuer/audience, allowed clock skew, and an optional
// revocation denylist.
type VerifierConfig struct {
	AllowedAlgs  []string
	HMACSecret   []byte
	RSAPublicKey *rsa.PublicKey
	Issuer       string
	Audience     string
	ClockSkew    time.Duration
	Denylist     TokenDenylist
}

// Verifier validates tokens against a fixed configuration. It is safe for
// concurrent use.
type Verifier struct {
	allowedAlgs  map[string]struct{}
	hmacSecret   []byte
	rsaPublicKey *rsa.PublicKey
	issuer       string
	audience     string
	clockSkew    time.Duration
	denylist     TokenDenylist
}

// NewVerifier validates the config and builds a Verifier. It rejects the "none"
// algorithm and any allowed algorithm whose key is missing, so a misconfigured
// verifier fails fast rather than silently accepting unsigned tokens.
func NewVerifier(config VerifierConfig) (*Verifier, error) {
	allowed := make(map[string]struct{}, len(config.AllowedAlgs))
	for _, alg := range config.AllowedAlgs {
		alg = strings.TrimSpace(alg)
		if alg == "" {
			continue
		}
		if strings.EqualFold(alg, "none") {
			return nil, fmt.Errorf("disallowed jwt algorithm: none")
		}
		allowed[alg] = struct{}{}
	}
	if len(allowed) == 0 {
		return nil, fmt.Errorf("no allowed jwt algorithms configured")
	}
	if _, ok := allowed[jwt.SigningMethodHS256.Alg()]; ok && len(config.HMACSecret) == 0 {
		return nil, fmt.Errorf("hs256 secret not configured")
	}
	if _, ok := allowed[jwt.SigningMethodRS256.Alg()]; ok && config.RSAPublicKey == nil {
		return nil, fmt.Errorf("rs256 public key not configured")
	}
	return &Verifier{
		allowedAlgs:  allowed,
		hmacSecret:   config.HMACSecret,
		rsaPublicKey: config.RSAPublicKey,
		issuer:       strings.TrimSpace(config.Issuer),
		audience:     strings.TrimSpace(config.Audience),
		clockSkew:    config.ClockSkew,
		denylist:     config.Denylist,
	}, nil
}

// NewVerifierFromEnv builds a Verifier from the JWT_* environment variables,
// wiring in the given denylist. HS256 secret falls back from JWT_HS256_SECRET to
// JWT_SECRET.
func NewVerifierFromEnv(denylist TokenDenylist) (*Verifier, error) {
	allowed := parseCommaList(config.GetEnv("JWT_ALLOWED_ALGS", "HS256"))
	clockSkew := config.GetEnvDuration("JWT_CLOCK_SKEW", 60*time.Second)
	secret := os.Getenv("JWT_HS256_SECRET")
	if secret == "" {
		secret = os.Getenv("JWT_SECRET")
	}
	publicKey, err := parseRSAPublicKey(os.Getenv("JWT_RS256_PUBLIC_KEY"))
	if err != nil {
		return nil, err
	}
	return NewVerifier(VerifierConfig{
		AllowedAlgs:  allowed,
		HMACSecret:   []byte(secret),
		RSAPublicKey: publicKey,
		Issuer:       os.Getenv("JWT_ISSUER"),
		Audience:     os.Getenv("JWT_AUDIENCE"),
		ClockSkew:    clockSkew,
		Denylist:     denylist,
	})
}

// Verify checks a token end to end: denylist, signing method and key, issuer,
// audience, and expiry (with configured clock skew), plus required registered
// claims (subject, iat, exp). It returns the parsed Claims or one of the
// Err* sentinels.
func (v *Verifier) Verify(ctx context.Context, tokenString string) (*Claims, error) {
	if strings.TrimSpace(tokenString) == "" {
		return nil, ErrTokenMissing
	}
	if v.denylist != nil {
		denied, err := v.denylist.IsTokenDenied(ctx, tokenString)
		if err != nil {
			return nil, ErrTokenInvalid
		}
		if denied {
			return nil, ErrTokenInvalid
		}
	}

	claims := &Claims{}
	options := []jwt.ParserOption{
		jwt.WithValidMethods(v.allowedMethods()),
		jwt.WithLeeway(v.clockSkew),
	}
	if v.issuer != "" {
		options = append(options, jwt.WithIssuer(v.issuer))
	}
	if v.audience != "" {
		options = append(options, jwt.WithAudience(v.audience))
	}
	parser := jwt.NewParser(options...)

	token, err := parser.ParseWithClaims(tokenString, claims, v.keyFunc)
	if err != nil || token == nil || !token.Valid {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, ErrTokenInvalid
	}

	if strings.TrimSpace(claims.Subject) == "" {
		return nil, ErrTokenInvalid
	}
	if claims.ExpiresAt == nil {
		return nil, ErrTokenInvalid
	}
	if claims.IssuedAt == nil {
		return nil, ErrTokenInvalid
	}
	if claims.IssuedAt.Time.After(time.Now().Add(v.clockSkew)) {
		return nil, ErrTokenInvalid
	}

	return claims, nil
}

func (v *Verifier) keyFunc(token *jwt.Token) (interface{}, error) {
	if token == nil || token.Method == nil {
		return nil, ErrTokenInvalid
	}
	alg := token.Method.Alg()
	if _, ok := v.allowedAlgs[alg]; !ok {
		return nil, ErrTokenInvalid
	}
	if strings.EqualFold(alg, "none") {
		return nil, ErrTokenInvalid
	}
	switch alg {
	case jwt.SigningMethodHS256.Alg():
		return v.hmacSecret, nil
	case jwt.SigningMethodRS256.Alg():
		return v.rsaPublicKey, nil
	default:
		return nil, ErrTokenInvalid
	}
}

func (v *Verifier) allowedMethods() []string {
	methods := make([]string, 0, len(v.allowedAlgs))
	for alg := range v.allowedAlgs {
		methods = append(methods, alg)
	}
	return methods
}

func parseRSAPublicKey(raw string) (*rsa.PublicKey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, fmt.Errorf("invalid rsa public key")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err == nil {
		if key, ok := pub.(*rsa.PublicKey); ok {
			return key, nil
		}
	}
	key, err := x509.ParsePKCS1PublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("invalid rsa public key")
	}
	return key, nil
}

func parseCommaList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

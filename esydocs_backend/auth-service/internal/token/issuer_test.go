package token

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestIssueAccessTokenValid(t *testing.T) {
	issuer := &Issuer{
		hmacSecret: []byte("test-secret-key-32-chars-long!!"),
		accessTTL:  time.Hour,
	}

	tokenStr, err := issuer.IssueAccessToken("user-123", "user", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tokenStr == "" {
		t.Fatal("expected non-empty token")
	}
}

func TestIssueAccessTokenNilIssuer(t *testing.T) {
	var issuer *Issuer
	_, err := issuer.IssueAccessToken("user-123", "user", nil)
	if err == nil {
		t.Error("expected error for nil issuer")
	}
}

func TestIssueAccessTokenEmptySecret(t *testing.T) {
	issuer := &Issuer{
		hmacSecret: nil,
		accessTTL:  time.Hour,
	}
	_, err := issuer.IssueAccessToken("user-123", "user", nil)
	if err == nil {
		t.Error("expected error for empty secret")
	}
}

func TestIssueAccessTokenClaims(t *testing.T) {
	secret := []byte("test-secret-key-32-chars-long!!")
	issuer := &Issuer{
		hmacSecret: secret,
		issuer:     "esydocs",
		audience:   "esydocs-api",
		accessTTL:  2 * time.Hour,
	}

	tokenStr, err := issuer.IssueAccessToken("user-456", "admin", []string{"read", "write"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Parse and verify claims
	parsed, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		return secret, nil
	})
	if err != nil {
		t.Fatalf("failed to parse token: %v", err)
	}

	claims, ok := parsed.Claims.(*Claims)
	if !ok {
		t.Fatal("failed to cast claims")
	}

	if claims.Subject != "user-456" {
		t.Errorf("expected subject 'user-456', got %q", claims.Subject)
	}
	if claims.Role != "admin" {
		t.Errorf("expected role 'admin', got %q", claims.Role)
	}
	if claims.Issuer != "esydocs" {
		t.Errorf("expected issuer 'esydocs', got %q", claims.Issuer)
	}
	if len(claims.Audience) != 1 || claims.Audience[0] != "esydocs-api" {
		t.Errorf("expected audience ['esydocs-api'], got %v", claims.Audience)
	}
	if len(claims.Scope) != 2 {
		t.Errorf("expected 2 scopes, got %d", len(claims.Scope))
	}
}

func TestIssueAccessTokenNoIssuerAudience(t *testing.T) {
	secret := []byte("test-secret-key-32-chars-long!!")
	issuer := &Issuer{
		hmacSecret: secret,
		accessTTL:  time.Hour,
	}

	tokenStr, err := issuer.IssueAccessToken("user-789", "user", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	parsed, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		return secret, nil
	})
	if err != nil {
		t.Fatalf("failed to parse token: %v", err)
	}

	claims := parsed.Claims.(*Claims)
	if claims.Issuer != "" {
		t.Errorf("expected empty issuer, got %q", claims.Issuer)
	}
	if len(claims.Audience) != 0 {
		t.Errorf("expected no audience, got %v", claims.Audience)
	}
}

func TestAccessTTL(t *testing.T) {
	issuer := &Issuer{
		hmacSecret: []byte("test"),
		accessTTL:  4 * time.Hour,
	}
	if got := issuer.AccessTTL(); got != 4*time.Hour {
		t.Errorf("expected 4h, got %v", got)
	}
}

func TestIssuerGetEnvDuration(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		fallback time.Duration
		want     time.Duration
	}{
		{"valid", "30m", time.Hour, 30 * time.Minute},
		{"empty uses fallback", "", 8 * time.Hour, 8 * time.Hour},
		{"invalid uses fallback", "invalid", 5 * time.Minute, 5 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TEST_DUR", tt.value)
			got := getEnvDuration("TEST_DUR", tt.fallback)
			if got != tt.want {
				t.Errorf("getEnvDuration(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

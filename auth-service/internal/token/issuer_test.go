package token

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestIssueAccessTokenValid(t *testing.T) {
	issuer := &Issuer{
		hmacSecret: []byte("test-secret-key-32-chars-long!!"),
		issuer:     "esydocs",
		audience:   "esydocs-api",
	}

	tokenStr, jti, expiresAt, err := issuer.IssueAccessToken("user-123", "user", nil, PlanInfo{Name: "free", MaxFileSizeMB: 25, MaxFilesPerJob: 10}, time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tokenStr == "" {
		t.Fatal("expected non-empty token")
	}
	if jti == "" {
		t.Fatal("expected non-empty jti")
	}
	if expiresAt.IsZero() {
		t.Fatal("expected non-zero expiresAt")
	}
}

func TestIssueAccessTokenNilIssuer(t *testing.T) {
	var issuer *Issuer
	_, _, _, err := issuer.IssueAccessToken("user-123", "user", nil, PlanInfo{}, time.Hour)
	if err == nil {
		t.Error("expected error for nil issuer")
	}
}

func TestIssueAccessTokenEmptySecret(t *testing.T) {
	issuer := &Issuer{
		hmacSecret: nil,
	}
	_, _, _, err := issuer.IssueAccessToken("user-123", "user", nil, PlanInfo{}, time.Hour)
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
	}

	tokenStr, _, _, err := issuer.IssueAccessToken("user-456", "admin", []string{"read", "write"}, PlanInfo{Name: "pro", MaxFileSizeMB: 500, MaxFilesPerJob: 50}, 2*time.Hour)
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

func TestIssueAccessTokenAlwaysSetsIssuerAudience(t *testing.T) {
	secret := []byte("test-secret-key-32-chars-long!!")
	issuer := &Issuer{
		hmacSecret: secret,
		issuer:     "esydocs",
		audience:   "esydocs-api",
	}

	tokenStr, _, _, err := issuer.IssueAccessToken("user-789", "user", nil, PlanInfo{}, time.Hour)
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
	if claims.Issuer != "esydocs" {
		t.Errorf("expected issuer 'esydocs', got %q", claims.Issuer)
	}
	if len(claims.Audience) != 1 || claims.Audience[0] != "esydocs-api" {
		t.Errorf("expected audience ['esydocs-api'], got %v", claims.Audience)
	}
	if claims.ID == "" {
		t.Error("expected non-empty JTI (token ID)")
	}
}

func TestNewIssuerFromEnvRequiresIssuerAndAudience(t *testing.T) {
	t.Setenv("JWT_HS256_SECRET", "test-secret-key-32-chars-long!!")

	t.Setenv("JWT_ISSUER", "")
	t.Setenv("JWT_AUDIENCE", "esydocs-api")
	_, err := NewIssuerFromEnv()
	if err == nil {
		t.Error("expected error when JWT_ISSUER is empty")
	}

	t.Setenv("JWT_ISSUER", "esydocs")
	t.Setenv("JWT_AUDIENCE", "")
	_, err = NewIssuerFromEnv()
	if err == nil {
		t.Error("expected error when JWT_AUDIENCE is empty")
	}

	t.Setenv("JWT_ISSUER", "esydocs")
	t.Setenv("JWT_AUDIENCE", "esydocs-api")
	issuer, err := NewIssuerFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issuer == nil {
		t.Fatal("expected non-nil issuer")
	}
}

func TestIssueAccessTokenEmbedsPlanInfo(t *testing.T) {
	secret := []byte("test-secret-key-32-chars-long!!")
	issuer := &Issuer{
		hmacSecret: secret,
		issuer:     "esydocs",
		audience:   "esydocs-api",
	}

	plan := PlanInfo{Name: "pro", MaxFileSizeMB: 500, MaxFilesPerJob: 50}
	tokenStr, _, _, err := issuer.IssueAccessToken("user-pro", "user", nil, plan, time.Hour)
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
	if claims.Plan != "pro" {
		t.Errorf("expected plan 'pro', got %q", claims.Plan)
	}
	if claims.PlanMaxFileSizeMB != 500 {
		t.Errorf("expected PlanMaxFileSizeMB 500, got %d", claims.PlanMaxFileSizeMB)
	}
	if claims.PlanMaxFilesPerJob != 50 {
		t.Errorf("expected PlanMaxFilesPerJob 50, got %d", claims.PlanMaxFilesPerJob)
	}
}

func TestIssueAccessTokenReturnsTTLDrivenExpiry(t *testing.T) {
	issuer := &Issuer{
		hmacSecret: []byte("test-secret-key-32-chars-long!!"),
		issuer:     "esydocs",
		audience:   "esydocs-api",
	}

	before := time.Now()
	_, _, expiresAt, err := issuer.IssueAccessToken("user-ttl", "user", nil, PlanInfo{}, 4*time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := before.Add(4 * time.Hour)
	if expiresAt.Before(expected.Add(-time.Second)) || expiresAt.After(expected.Add(time.Second)) {
		t.Errorf("expiresAt %v not within 1s of expected %v", expiresAt, expected)
	}
}

func TestIssueRefreshTokenValid(t *testing.T) {
	issuer := &Issuer{
		hmacSecret: []byte("test-secret-key-32-chars-long!!"),
		issuer:     "esydocs",
		audience:   "esydocs-api",
	}

	tokenStr, jti, expiresAt, err := issuer.IssueRefreshToken("user-123", 7*24*time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tokenStr == "" {
		t.Fatal("expected non-empty token")
	}
	if jti == "" {
		t.Fatal("expected non-empty jti")
	}

	expected := time.Now().Add(7 * 24 * time.Hour)
	if expiresAt.Before(expected.Add(-time.Second)) || expiresAt.After(expected.Add(time.Second)) {
		t.Errorf("expiresAt %v not within 1s of expected %v", expiresAt, expected)
	}
}

func TestIssueRefreshTokenNilIssuer(t *testing.T) {
	var issuer *Issuer
	_, _, _, err := issuer.IssueRefreshToken("user-123", time.Hour)
	if err == nil {
		t.Error("expected error for nil issuer")
	}
}

func TestVerifyRefreshTokenValid(t *testing.T) {
	issuer := &Issuer{
		hmacSecret: []byte("test-secret-key-32-chars-long!!"),
		issuer:     "esydocs",
		audience:   "esydocs-api",
	}

	tokenStr, _, _, err := issuer.IssueRefreshToken("user-456", time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	userID, err := issuer.VerifyRefreshToken(tokenStr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if userID != "user-456" {
		t.Errorf("expected userID 'user-456', got %q", userID)
	}
}

func TestVerifyRefreshTokenInvalid(t *testing.T) {
	issuer := &Issuer{
		hmacSecret: []byte("test-secret-key-32-chars-long!!"),
		issuer:     "esydocs",
		audience:   "esydocs-api",
	}

	_, err := issuer.VerifyRefreshToken("invalid-token")
	if err == nil {
		t.Error("expected error for invalid token")
	}
}

func TestVerifyRefreshTokenWrongSecret(t *testing.T) {
	issuer1 := &Issuer{
		hmacSecret: []byte("test-secret-key-32-chars-long!!"),
		issuer:     "esydocs",
		audience:   "esydocs-api",
	}
	issuer2 := &Issuer{
		hmacSecret: []byte("different-secret-key-32-chars!!!"),
		issuer:     "esydocs",
		audience:   "esydocs-api",
	}

	tokenStr, _, _, _ := issuer1.IssueRefreshToken("user-789", time.Hour)
	_, err := issuer2.VerifyRefreshToken(tokenStr)
	if err == nil {
		t.Error("expected error for wrong secret")
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

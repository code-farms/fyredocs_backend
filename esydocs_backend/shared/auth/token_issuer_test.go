package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// newTestIssuer builds an Issuer directly (bypassing NewIssuerFromEnv) so
// that tests do not depend on environment variables.
func newTestIssuer(secret []byte, ttl time.Duration) *Issuer {
	return &Issuer{
		hmacSecret: secret,
		issuer:     "test-issuer",
		audience:   "test-audience",
		accessTTL:  ttl,
	}
}

func TestIssueAccessToken(t *testing.T) {
	secret := []byte("issuer-test-secret-key-1234567890")
	issuer := newTestIssuer(secret, 1*time.Hour)

	tokenStr, err := issuer.IssueAccessToken("user-456", "user", nil)
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	if tokenStr == "" {
		t.Fatal("expected non-empty token string")
	}

	// Parse the token back and inspect the claims.
	parser := jwt.NewParser(jwt.WithValidMethods([]string{"HS256"}))
	claims := &Claims{}
	token, err := parser.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (interface{}, error) {
		return secret, nil
	})
	if err != nil {
		t.Fatalf("ParseWithClaims: %v", err)
	}
	if !token.Valid {
		t.Fatal("parsed token is not valid")
	}

	if claims.Subject != "user-456" {
		t.Errorf("subject: got %q, want %q", claims.Subject, "user-456")
	}
	if claims.Role != "user" {
		t.Errorf("role: got %q, want %q", claims.Role, "user")
	}
	if claims.Issuer != "test-issuer" {
		t.Errorf("issuer: got %q, want %q", claims.Issuer, "test-issuer")
	}
	if len(claims.Audience) != 1 || claims.Audience[0] != "test-audience" {
		t.Errorf("audience: got %v, want [test-audience]", claims.Audience)
	}
	if claims.ExpiresAt == nil {
		t.Fatal("expected ExpiresAt to be set")
	}
	if claims.IssuedAt == nil {
		t.Fatal("expected IssuedAt to be set")
	}

	// Verify the expiry is approximately 1 hour from now.
	expectedExpiry := time.Now().Add(1 * time.Hour)
	diff := claims.ExpiresAt.Time.Sub(expectedExpiry)
	if diff < -5*time.Second || diff > 5*time.Second {
		t.Errorf("ExpiresAt drift too large: %v (expected ~1h from now)", diff)
	}
}

func TestIssueTokenWithScopes(t *testing.T) {
	secret := []byte("issuer-test-secret-key-1234567890")
	issuer := newTestIssuer(secret, 30*time.Minute)

	scopes := []string{"pdf:read", "pdf:write", "pdf:optimize"}
	tokenStr, err := issuer.IssueAccessToken("user-789", "editor", scopes)
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	parser := jwt.NewParser(jwt.WithValidMethods([]string{"HS256"}))
	claims := &Claims{}
	token, err := parser.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (interface{}, error) {
		return secret, nil
	})
	if err != nil {
		t.Fatalf("ParseWithClaims: %v", err)
	}
	if !token.Valid {
		t.Fatal("parsed token is not valid")
	}

	if claims.Role != "editor" {
		t.Errorf("role: got %q, want %q", claims.Role, "editor")
	}
	if len(claims.Scope) != len(scopes) {
		t.Fatalf("scope length: got %d, want %d", len(claims.Scope), len(scopes))
	}
	for i, s := range scopes {
		if claims.Scope[i] != s {
			t.Errorf("scope[%d]: got %q, want %q", i, claims.Scope[i], s)
		}
	}
}

func TestIssueAccessTokenEmptyScopes(t *testing.T) {
	secret := []byte("issuer-test-secret-key-1234567890")
	issuer := newTestIssuer(secret, 1*time.Hour)

	tokenStr, err := issuer.IssueAccessToken("user-000", "viewer", []string{})
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	parser := jwt.NewParser(jwt.WithValidMethods([]string{"HS256"}))
	claims := &Claims{}
	_, err = parser.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (interface{}, error) {
		return secret, nil
	})
	if err != nil {
		t.Fatalf("ParseWithClaims: %v", err)
	}

	if len(claims.Scope) != 0 {
		t.Errorf("expected empty scope, got %v", claims.Scope)
	}
}

func TestIssueAccessTokenNilIssuer(t *testing.T) {
	var issuer *Issuer

	_, err := issuer.IssueAccessToken("user-1", "user", nil)
	if err == nil {
		t.Fatal("expected error when issuer is nil, got nil")
	}
}

func TestIssueAccessTokenRoundTrip(t *testing.T) {
	// Issue a token, then verify it through the Verifier to ensure end-to-end
	// compatibility between Issuer and Verifier.
	secret := []byte("round-trip-secret-key-1234567890")
	issuer := newTestIssuer(secret, 1*time.Hour)

	tokenStr, err := issuer.IssueAccessToken("user-rt", "admin", []string{"all"})
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	verifier, err := NewVerifier(VerifierConfig{
		AllowedAlgs: []string{"HS256"},
		HMACSecret:  secret,
		Issuer:      "test-issuer",
		Audience:    "test-audience",
		ClockSkew:   30 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	claims, err := verifier.Verify(t.Context(), tokenStr)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "user-rt" {
		t.Errorf("subject: got %q, want %q", claims.Subject, "user-rt")
	}
	if claims.Role != "admin" {
		t.Errorf("role: got %q, want %q", claims.Role, "admin")
	}
}

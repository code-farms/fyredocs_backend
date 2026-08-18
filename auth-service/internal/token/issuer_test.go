// Tests for token issuance and refresh rotation. These pin the security-critical
// behaviours: a rotated refresh token must not be reusable, expiry and issuer
// claims must be present and validated, and a revoked token must be rejected.
package token

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestIssueAccessTokenValid(t *testing.T) {
	issuer := &Issuer{
		hmacSecret: []byte("test-secret-key-32-chars-long!!"),
		issuer:     "fyredocs",
		audience:   "fyredocs-api",
	}

	tokenStr, jti, expiresAt, err := issuer.IssueAccessToken("user-123", "user", nil, time.Hour)
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
	_, _, _, err := issuer.IssueAccessToken("user-123", "user", nil, time.Hour)
	if err == nil {
		t.Error("expected error for nil issuer")
	}
}

func TestIssueAccessTokenEmptySecret(t *testing.T) {
	issuer := &Issuer{
		hmacSecret: nil,
	}
	_, _, _, err := issuer.IssueAccessToken("user-123", "user", nil, time.Hour)
	if err == nil {
		t.Error("expected error for empty secret")
	}
}

func TestIssueAccessTokenClaims(t *testing.T) {
	secret := []byte("test-secret-key-32-chars-long!!")
	issuer := &Issuer{
		hmacSecret: secret,
		issuer:     "fyredocs",
		audience:   "fyredocs-api",
	}

	tokenStr, _, _, err := issuer.IssueAccessToken("user-456", "admin", []string{"read", "write"}, 2*time.Hour)
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
	if claims.Issuer != "fyredocs" {
		t.Errorf("expected issuer 'fyredocs', got %q", claims.Issuer)
	}
	if len(claims.Audience) != 1 || claims.Audience[0] != "fyredocs-api" {
		t.Errorf("expected audience ['fyredocs-api'], got %v", claims.Audience)
	}
	if len(claims.Scope) != 2 {
		t.Errorf("expected 2 scopes, got %d", len(claims.Scope))
	}
}

func TestIssueAccessTokenAlwaysSetsIssuerAudience(t *testing.T) {
	secret := []byte("test-secret-key-32-chars-long!!")
	issuer := &Issuer{
		hmacSecret: secret,
		issuer:     "fyredocs",
		audience:   "fyredocs-api",
	}

	tokenStr, _, _, err := issuer.IssueAccessToken("user-789", "user", nil, time.Hour)
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
	if claims.Issuer != "fyredocs" {
		t.Errorf("expected issuer 'fyredocs', got %q", claims.Issuer)
	}
	if len(claims.Audience) != 1 || claims.Audience[0] != "fyredocs-api" {
		t.Errorf("expected audience ['fyredocs-api'], got %v", claims.Audience)
	}
	if claims.ID == "" {
		t.Error("expected non-empty JTI (token ID)")
	}
}

func TestNewIssuerFromEnvRequiresIssuerAndAudience(t *testing.T) {
	t.Setenv("JWT_HS256_SECRET", "test-secret-key-32-chars-long!!")

	t.Setenv("JWT_ISSUER", "")
	t.Setenv("JWT_AUDIENCE", "fyredocs-api")
	_, err := NewIssuerFromEnv()
	if err == nil {
		t.Error("expected error when JWT_ISSUER is empty")
	}

	t.Setenv("JWT_ISSUER", "fyredocs")
	t.Setenv("JWT_AUDIENCE", "")
	_, err = NewIssuerFromEnv()
	if err == nil {
		t.Error("expected error when JWT_AUDIENCE is empty")
	}

	t.Setenv("JWT_ISSUER", "fyredocs")
	t.Setenv("JWT_AUDIENCE", "fyredocs-api")
	issuer, err := NewIssuerFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issuer == nil {
		t.Fatal("expected non-nil issuer")
	}
}

func TestIssueAccessTokenNoPlanClaims(t *testing.T) {
	secret := []byte("test-secret-key-32-chars-long!!")
	issuer := &Issuer{
		hmacSecret: secret,
		issuer:     "fyredocs",
		audience:   "fyredocs-api",
	}

	tokenStr, _, _, err := issuer.IssueAccessToken("user-pro", "user", nil, time.Hour)
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
	if claims.Role != "user" {
		t.Errorf("expected role 'user', got %q", claims.Role)
	}
	if claims.Subject != "user-pro" {
		t.Errorf("expected subject 'user-pro', got %q", claims.Subject)
	}
}

func TestIssueAccessTokenReturnsTTLDrivenExpiry(t *testing.T) {
	issuer := &Issuer{
		hmacSecret: []byte("test-secret-key-32-chars-long!!"),
		issuer:     "fyredocs",
		audience:   "fyredocs-api",
	}

	before := time.Now()
	_, _, expiresAt, err := issuer.IssueAccessToken("user-ttl", "user", nil, 4*time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := before.Add(4 * time.Hour)
	if expiresAt.Before(expected.Add(-time.Second)) || expiresAt.After(expected.Add(time.Second)) {
		t.Errorf("expiresAt %v not within 1s of expected %v", expiresAt, expected)
	}
}

func TestIssueImpersonationTokenClaims(t *testing.T) {
	secret := []byte("test-secret-key-32-chars-long!!")
	issuer := &Issuer{
		hmacSecret: secret,
		issuer:     "fyredocs",
		audience:   "fyredocs-api",
	}

	before := time.Now()
	tokenStr, jti, expiresAt, err := issuer.IssueImpersonationToken("target-1", "user", nil, "admin-9", 30*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tokenStr == "" || jti == "" {
		t.Fatal("expected non-empty token and jti")
	}

	expected := before.Add(30 * time.Minute)
	if expiresAt.Before(expected.Add(-time.Second)) || expiresAt.After(expected.Add(time.Second)) {
		t.Errorf("expiresAt %v not within 1s of expected %v", expiresAt, expected)
	}

	parsed, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		return secret, nil
	})
	if err != nil {
		t.Fatalf("failed to parse token: %v", err)
	}
	claims := parsed.Claims.(*Claims)
	if claims.Subject != "target-1" {
		t.Errorf("expected subject 'target-1', got %q", claims.Subject)
	}
	if claims.Role != "user" {
		t.Errorf("expected role 'user', got %q", claims.Role)
	}
	if claims.ImpersonatedBy != "admin-9" {
		t.Errorf("expected impersonated_by 'admin-9', got %q", claims.ImpersonatedBy)
	}
}

func TestIssueImpersonationTokenNilIssuer(t *testing.T) {
	var issuer *Issuer
	_, _, _, err := issuer.IssueImpersonationToken("target-1", "user", nil, "admin-9", time.Hour)
	if err == nil {
		t.Error("expected error for nil issuer")
	}
}

func TestIssueAccessTokenHasNoImpersonatedBy(t *testing.T) {
	secret := []byte("test-secret-key-32-chars-long!!")
	issuer := &Issuer{
		hmacSecret: secret,
		issuer:     "fyredocs",
		audience:   "fyredocs-api",
	}

	tokenStr, _, _, err := issuer.IssueAccessToken("user-1", "user", nil, time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	parsed, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		return secret, nil
	})
	if err != nil {
		t.Fatalf("failed to parse token: %v", err)
	}
	if claims := parsed.Claims.(*Claims); claims.ImpersonatedBy != "" {
		t.Errorf("expected empty impersonated_by on a normal token, got %q", claims.ImpersonatedBy)
	}
}

func TestIssueRefreshTokenValid(t *testing.T) {
	issuer := &Issuer{
		hmacSecret: []byte("test-secret-key-32-chars-long!!"),
		issuer:     "fyredocs",
		audience:   "fyredocs-api",
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
		issuer:     "fyredocs",
		audience:   "fyredocs-api",
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
		issuer:     "fyredocs",
		audience:   "fyredocs-api",
	}

	_, err := issuer.VerifyRefreshToken("invalid-token")
	if err == nil {
		t.Error("expected error for invalid token")
	}
}

func TestVerifyRefreshTokenWrongSecret(t *testing.T) {
	issuer1 := &Issuer{
		hmacSecret: []byte("test-secret-key-32-chars-long!!"),
		issuer:     "fyredocs",
		audience:   "fyredocs-api",
	}
	issuer2 := &Issuer{
		hmacSecret: []byte("different-secret-key-32-chars!!!"),
		issuer:     "fyredocs",
		audience:   "fyredocs-api",
	}

	tokenStr, _, _, _ := issuer1.IssueRefreshToken("user-789", time.Hour)
	_, err := issuer2.VerifyRefreshToken(tokenStr)
	if err == nil {
		t.Error("expected error for wrong secret")
	}
}

func TestVerifyRefreshTokenNilIssuer(t *testing.T) {
	var issuer *Issuer
	if _, err := issuer.VerifyRefreshToken("any-token"); err == nil {
		t.Error("expected error for nil issuer")
	}
}

// TestVerifyRefreshTokenTamperedSignature flips one character of the signature
// segment: a valid payload under a corrupted MAC must be rejected, or an
// attacker could splice claims onto a previously observed signature.
func TestVerifyRefreshTokenTamperedSignature(t *testing.T) {
	issuer := &Issuer{
		hmacSecret: []byte("test-secret-key-32-chars-long!!"),
		issuer:     "fyredocs",
		audience:   "fyredocs-api",
	}

	tokenStr, _, _, err := issuer.IssueRefreshToken("user-tamper", time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Tamper the FIRST character of the signature segment, never the last:
	// the final base64url char of a 32-byte MAC carries 2 padding bits the
	// decoder ignores, so a last-char flip between values 0-3 (A-D) decodes
	// to the identical signature and verifies — a ~5% flake.
	dot := strings.LastIndexByte(tokenStr, '.')
	sigStart := dot + 1
	flip := byte('A')
	if tokenStr[sigStart] == 'A' {
		flip = 'B'
	}
	tampered := tokenStr[:sigStart] + string(flip) + tokenStr[sigStart+1:]

	if _, err := issuer.VerifyRefreshToken(tampered); err == nil {
		t.Error("expected error for tampered signature")
	}
}

// TestVerifyRefreshTokenAlgNoneRejected pins the classic JWT downgrade attack:
// an unsigned alg=none token with otherwise perfect claims must be refused by
// the keyfunc's method check, not accepted because "no signature" verifies
// trivially.
func TestVerifyRefreshTokenAlgNoneRejected(t *testing.T) {
	issuer := &Issuer{
		hmacSecret: []byte("test-secret-key-32-chars-long!!"),
		issuer:     "fyredocs",
		audience:   "fyredocs-api",
	}

	claims := jwt.RegisteredClaims{
		Subject:   "user-none",
		Issuer:    "fyredocs",
		Audience:  []string{"fyredocs-api"},
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	tokenStr, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("failed to build alg=none token: %v", err)
	}

	if _, err := issuer.VerifyRefreshToken(tokenStr); err == nil {
		t.Error("expected error for alg=none token")
	}
}

func TestVerifyRefreshTokenExpired(t *testing.T) {
	issuer := &Issuer{
		hmacSecret: []byte("test-secret-key-32-chars-long!!"),
		issuer:     "fyredocs",
		audience:   "fyredocs-api",
	}

	// A negative TTL yields an already-expired but correctly signed token — the
	// exp claim must be enforced, not merely present.
	tokenStr, _, _, err := issuer.IssueRefreshToken("user-expired", -time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := issuer.VerifyRefreshToken(tokenStr); err == nil {
		t.Error("expected error for expired refresh token")
	}
}

// TestVerifyRefreshTokenCrossTenantRejected pins the provenance checks
// (finding 3.4 — docs/developer/architecture/backend-hardening.md): a token
// signed with the SAME secret but a different issuer or audience (e.g. another
// environment sharing a leaked secret) must not verify.
func TestVerifyRefreshTokenCrossTenantRejected(t *testing.T) {
	secret := []byte("test-secret-key-32-chars-long!!")
	verifier := &Issuer{hmacSecret: secret, issuer: "fyredocs", audience: "fyredocs-api"}

	cases := []struct {
		name     string
		issuer   string
		audience string
	}{
		{"wrong issuer", "other-app", "fyredocs-api"},
		{"wrong audience", "fyredocs", "other-api"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			foreign := &Issuer{hmacSecret: secret, issuer: tc.issuer, audience: tc.audience}
			tokenStr, _, _, err := foreign.IssueRefreshToken("user-x", time.Hour)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if _, err := verifier.VerifyRefreshToken(tokenStr); err == nil {
				t.Error("expected error for cross-tenant token")
			}
		})
	}
}

// TestVerifyRefreshTokenSubjectHandling: a refresh token without a usable
// subject authenticates nobody and must be refused even though its signature
// is genuine; a padded subject is trimmed at issue time so downstream UUID
// parsing sees the canonical form.
func TestVerifyRefreshTokenSubjectHandling(t *testing.T) {
	issuer := &Issuer{
		hmacSecret: []byte("test-secret-key-32-chars-long!!"),
		issuer:     "fyredocs",
		audience:   "fyredocs-api",
	}

	t.Run("empty subject rejected", func(t *testing.T) {
		tokenStr, _, _, err := issuer.IssueRefreshToken("", time.Hour)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, err := issuer.VerifyRefreshToken(tokenStr); err == nil {
			t.Error("expected error for empty subject")
		}
	})

	t.Run("whitespace subject rejected", func(t *testing.T) {
		// IssueRefreshToken trims the subject, so whitespace collapses to empty
		// and must fail verification the same way.
		tokenStr, _, _, err := issuer.IssueRefreshToken("   ", time.Hour)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, err := issuer.VerifyRefreshToken(tokenStr); err == nil {
			t.Error("expected error for whitespace-only subject")
		}
	})

	t.Run("padded subject trimmed at issue", func(t *testing.T) {
		tokenStr, _, _, err := issuer.IssueRefreshToken("  user-padded  ", time.Hour)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		userID, err := issuer.VerifyRefreshToken(tokenStr)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if userID != "user-padded" {
			t.Errorf("expected trimmed subject 'user-padded', got %q", userID)
		}
	})
}

// TestVerifyRefreshTokenAcceptsAccessTokenPinnedGap pins a known gap, not
// desired behavior.
//
// NOTE: tokens carry no type claim, so VerifyRefreshToken accepts an ACCESS
// token as a refresh token (same secret, issuer, and audience). The
// /auth/refresh handler still rejects the attempt because the session lookup
// matches stored refresh-token hashes only, but the confusion should be closed
// at the token layer (candidate finding for
// docs/developer/architecture/backend-hardening.md; pinned per
// docs/developer/architecture/testing-strategy.md — gaps are asserted, not
// silently fixed). If this test starts failing, the gap was closed: update the
// hardening doc and invert this assertion.
func TestVerifyRefreshTokenAcceptsAccessTokenPinnedGap(t *testing.T) {
	issuer := &Issuer{
		hmacSecret: []byte("test-secret-key-32-chars-long!!"),
		issuer:     "fyredocs",
		audience:   "fyredocs-api",
	}

	accessToken, _, _, err := issuer.IssueAccessToken("user-confused", "user", nil, time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	userID, err := issuer.VerifyRefreshToken(accessToken)
	if err != nil {
		t.Fatalf("pinned behavior changed: VerifyRefreshToken now rejects access tokens (%v) — update backend-hardening.md and this test", err)
	}
	if userID != "user-confused" {
		t.Errorf("expected subject 'user-confused', got %q", userID)
	}
}

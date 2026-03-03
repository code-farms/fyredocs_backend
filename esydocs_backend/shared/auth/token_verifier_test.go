package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// testSecret is the HMAC secret used across verifier tests.
var testSecret = []byte("test-secret-key-for-unit-tests-1234")

// newHS256Verifier creates a Verifier configured for HS256 with the given
// options. It fails the test immediately if construction returns an error.
func newHS256Verifier(t *testing.T, secret []byte, denylist TokenDenylist) *Verifier {
	t.Helper()
	v, err := NewVerifier(VerifierConfig{
		AllowedAlgs: []string{"HS256"},
		HMACSecret:  secret,
		ClockSkew:   30 * time.Second,
		Denylist:    denylist,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

// issueHS256Token builds a signed HS256 JWT from the provided claims using the
// given secret. It fails the test if signing returns an error.
func issueHS256Token(t *testing.T, claims jwt.Claims, secret []byte) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(secret)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}
	return signed
}

// validClaims returns a Claims value that satisfies all Verifier checks
// (Subject, IssuedAt, ExpiresAt populated, not expired).
func validClaims() Claims {
	now := time.Now()
	return Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-123",
			IssuedAt:  jwt.NewNumericDate(now.Add(-1 * time.Minute)),
			ExpiresAt: jwt.NewNumericDate(now.Add(1 * time.Hour)),
		},
		Role:  "user",
		Scope: ScopeList{"read", "write"},
	}
}

// --- mock denylist ----------------------------------------------------------

type mockDenylist struct {
	denied map[string]bool
	err    error
}

func (m *mockDenylist) IsTokenDenied(_ context.Context, token string) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	return m.denied[token], nil
}

func (m *mockDenylist) DenyToken(_ context.Context, token string, _ time.Duration) error {
	if m.denied == nil {
		m.denied = make(map[string]bool)
	}
	m.denied[token] = true
	return nil
}

// --- tests ------------------------------------------------------------------

func TestVerifyValidHS256Token(t *testing.T) {
	v := newHS256Verifier(t, testSecret, nil)
	tokenStr := issueHS256Token(t, validClaims(), testSecret)

	claims, err := v.Verify(context.Background(), tokenStr)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if claims.Subject != "user-123" {
		t.Errorf("subject: got %q, want %q", claims.Subject, "user-123")
	}
	if claims.Role != "user" {
		t.Errorf("role: got %q, want %q", claims.Role, "user")
	}
	if len(claims.Scope) != 2 || claims.Scope[0] != "read" || claims.Scope[1] != "write" {
		t.Errorf("scope: got %v, want [read write]", claims.Scope)
	}
}

func TestVerifyExpiredToken(t *testing.T) {
	v := newHS256Verifier(t, testSecret, nil)

	c := validClaims()
	// Set both timestamps in the past so the token is clearly expired even
	// after accounting for the 30-second clock-skew leeway.
	c.IssuedAt = jwt.NewNumericDate(time.Now().Add(-2 * time.Hour))
	c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-1 * time.Hour))

	tokenStr := issueHS256Token(t, c, testSecret)

	_, err := v.Verify(context.Background(), tokenStr)
	if err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
	if !errors.Is(err, ErrTokenExpired) {
		t.Errorf("expected ErrTokenExpired, got %v", err)
	}
}

func TestVerifyInvalidSignature(t *testing.T) {
	v := newHS256Verifier(t, testSecret, nil)

	// Sign with a different secret so the signature does not match.
	wrongSecret := []byte("wrong-secret-completely-different")
	tokenStr := issueHS256Token(t, validClaims(), wrongSecret)

	_, err := v.Verify(context.Background(), tokenStr)
	if err == nil {
		t.Fatal("expected error for invalid signature, got nil")
	}
	if !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("expected ErrTokenInvalid, got %v", err)
	}
}

func TestVerifyDeniedToken(t *testing.T) {
	tokenStr := issueHS256Token(t, validClaims(), testSecret)

	deny := &mockDenylist{
		denied: map[string]bool{tokenStr: true},
	}
	v := newHS256Verifier(t, testSecret, deny)

	_, err := v.Verify(context.Background(), tokenStr)
	if err == nil {
		t.Fatal("expected error for denied token, got nil")
	}
	if !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("expected ErrTokenInvalid, got %v", err)
	}
}

func TestVerifyDeniedTokenDenylistError(t *testing.T) {
	tokenStr := issueHS256Token(t, validClaims(), testSecret)

	deny := &mockDenylist{
		err: errors.New("redis down"),
	}
	v := newHS256Verifier(t, testSecret, deny)

	_, err := v.Verify(context.Background(), tokenStr)
	if err == nil {
		t.Fatal("expected error when denylist returns error, got nil")
	}
	if !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("expected ErrTokenInvalid, got %v", err)
	}
}

func TestVerifyMalformedToken(t *testing.T) {
	v := newHS256Verifier(t, testSecret, nil)

	cases := []struct {
		name  string
		token string
	}{
		{"garbage", "not-a-jwt-at-all"},
		{"empty", ""},
		{"spaces", "   "},
		{"partial", "eyJhbGciOiJIUzI1NiJ9."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := v.Verify(context.Background(), tc.token)
			if err == nil {
				t.Fatalf("expected error for malformed token %q, got nil", tc.token)
			}
		})
	}
}

func TestVerifyTokenMissingSubject(t *testing.T) {
	v := newHS256Verifier(t, testSecret, nil)

	now := time.Now()
	c := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "", // empty subject
			IssuedAt:  jwt.NewNumericDate(now.Add(-1 * time.Minute)),
			ExpiresAt: jwt.NewNumericDate(now.Add(1 * time.Hour)),
		},
		Role: "user",
	}
	tokenStr := issueHS256Token(t, c, testSecret)

	_, err := v.Verify(context.Background(), tokenStr)
	if err == nil {
		t.Fatal("expected error for token without subject, got nil")
	}
	if !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("expected ErrTokenInvalid, got %v", err)
	}
}

func TestVerifyTokenMissingExpiresAt(t *testing.T) {
	v := newHS256Verifier(t, testSecret, nil)

	now := time.Now()
	c := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:  "user-123",
			IssuedAt: jwt.NewNumericDate(now.Add(-1 * time.Minute)),
			// no ExpiresAt
		},
		Role: "user",
	}
	tokenStr := issueHS256Token(t, c, testSecret)

	_, err := v.Verify(context.Background(), tokenStr)
	if err == nil {
		t.Fatal("expected error for token without ExpiresAt, got nil")
	}
}

func TestNewVerifierRejectsNoneAlg(t *testing.T) {
	_, err := NewVerifier(VerifierConfig{
		AllowedAlgs: []string{"none"},
		HMACSecret:  testSecret,
	})
	if err == nil {
		t.Fatal("expected error when 'none' algorithm is allowed, got nil")
	}
}

func TestNewVerifierRejectsEmptyAlgs(t *testing.T) {
	_, err := NewVerifier(VerifierConfig{
		AllowedAlgs: []string{},
		HMACSecret:  testSecret,
	})
	if err == nil {
		t.Fatal("expected error when no algorithms are configured, got nil")
	}
}

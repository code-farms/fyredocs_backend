package authverify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// fakeGuestStore validates a single known guest token.
type fakeGuestStore struct {
	valid string
	err   error
}

func (f *fakeGuestStore) ValidateGuestToken(_ context.Context, token string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return token == f.valid, nil
}

func ginTestVerifier(t *testing.T) (*Verifier, string) {
	t.Helper()
	secret := []byte("test-secret-key-32-chars-long!!")
	v, err := NewVerifier(VerifierConfig{AllowedAlgs: []string{"HS256"}, HMACSecret: secret})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-123",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
		},
		Role: "user",
	}
	tokenStr, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		t.Fatal(err)
	}
	return v, tokenStr
}

// ginHarness mounts the middleware plus a probe handler that records the
// AuthContext each request carried.
func ginHarness(options GinMiddlewareOptions, got *AuthContext, ok *bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(GinAuthMiddleware(options))
	handler := func(c *gin.Context) {
		*got, *ok = GetGinAuth(c)
		c.Status(http.StatusOK)
	}
	r.GET("/probe", handler)
	r.OPTIONS("/probe", handler)
	return r
}

func TestGinAuthMiddlewareValidBearer(t *testing.T) {
	v, token := ginTestVerifier(t)
	var got AuthContext
	var ok bool
	r := ginHarness(GinMiddlewareOptions{Verifier: v}, &got, &ok)

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !ok || got.UserID != "user-123" || got.Role != "user" {
		t.Errorf("auth context = %+v (ok=%v), want user-123/user", got, ok)
	}
}

func TestGinAuthMiddlewareInvalidToken(t *testing.T) {
	v, _ := ginTestVerifier(t)
	var got AuthContext
	var ok bool
	r := ginHarness(GinMiddlewareOptions{Verifier: v}, &got, &ok)

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("Authorization", "Bearer not-a-jwt")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestGinAuthMiddlewareAccessTokenCookieFallback(t *testing.T) {
	v, token := ginTestVerifier(t)
	var got AuthContext
	var ok bool
	r := ginHarness(GinMiddlewareOptions{Verifier: v}, &got, &ok)

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: token})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !ok || got.UserID != "user-123" {
		t.Errorf("auth context = %+v (ok=%v), want user-123 from cookie", got, ok)
	}
}

func TestGinAuthMiddlewareNoTokenPassesThroughWithoutAuth(t *testing.T) {
	v, _ := ginTestVerifier(t)
	var got AuthContext
	var ok bool
	r := ginHarness(GinMiddlewareOptions{Verifier: v}, &got, &ok)

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (fail-open with no auth context)", w.Code)
	}
	if ok {
		t.Errorf("expected no auth context for anonymous request, got %+v", got)
	}
}

func TestGinAuthMiddlewareGuestHeaderBeforeCookie(t *testing.T) {
	v, _ := ginTestVerifier(t)
	store := &fakeGuestStore{valid: "guest-abc"}
	var got AuthContext
	var ok bool
	r := ginHarness(GinMiddlewareOptions{Verifier: v, GuestStore: store}, &got, &ok)

	// Header token is the valid one; cookie holds garbage — header must win.
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("X-Guest-Token", "guest-abc")
	req.AddCookie(&http.Cookie{Name: "guest_token", Value: "bogus"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !ok || !got.IsGuest || got.Role != "guest" {
		t.Errorf("auth context = %+v (ok=%v), want guest via header", got, ok)
	}
}

func TestGinAuthMiddlewareGuestCookieFallback(t *testing.T) {
	v, _ := ginTestVerifier(t)
	store := &fakeGuestStore{valid: "guest-abc"}
	var got AuthContext
	var ok bool
	r := ginHarness(GinMiddlewareOptions{Verifier: v, GuestStore: store}, &got, &ok)

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.AddCookie(&http.Cookie{Name: "guest_token", Value: "guest-abc"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !ok || !got.IsGuest {
		t.Errorf("auth context = %+v (ok=%v), want guest via cookie", got, ok)
	}
}

func TestGinAuthMiddlewareTrustGatewayHeaders(t *testing.T) {
	var got AuthContext
	var ok bool
	r := ginHarness(GinMiddlewareOptions{TrustGatewayHeaders: true}, &got, &ok)

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("X-User-ID", "user-777")
	req.Header.Set("X-User-Role", "admin")
	req.Header.Set("X-User-Plan", "pro")
	req.Header.Set("X-User-Plan-Max-File-MB", "500")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !ok || got.UserID != "user-777" || got.Role != "admin" {
		t.Errorf("auth context = %+v (ok=%v), want gateway identity", got, ok)
	}
	if got.Plan != "pro" || got.PlanMaxFileSizeMB != 500 {
		t.Errorf("plan fields = %q/%d, want pro/500 (shared header parser fills plan)", got.Plan, got.PlanMaxFileSizeMB)
	}
}

func TestGinAuthMiddlewareOptionsPassthrough(t *testing.T) {
	v, _ := ginTestVerifier(t)
	var got AuthContext
	var ok bool
	r := ginHarness(GinMiddlewareOptions{Verifier: v}, &got, &ok)

	// A garbage bearer token on OPTIONS must not 401 — preflight skips auth.
	req := httptest.NewRequest(http.MethodOptions, "/probe", nil)
	req.Header.Set("Authorization", "Bearer garbage")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for OPTIONS passthrough", w.Code)
	}
}

func TestGinAuthMiddlewareNilVerifierRejectsToken(t *testing.T) {
	var got AuthContext
	var ok bool
	r := ginHarness(GinMiddlewareOptions{}, &got, &ok)

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 when a token arrives with no verifier", w.Code)
	}
}

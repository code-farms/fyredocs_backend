package authverify

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestExtractBearerToken(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
		wantOk bool
	}{
		{"valid bearer", "Bearer mytoken123", "mytoken123", true},
		{"empty header", "", "", false},
		{"missing prefix", "mytoken123", "", false},
		{"wrong prefix", "Basic mytoken123", "", false},
		{"bearer only", "Bearer ", "", false},
		{"case insensitive bearer", "bearer mytoken123", "mytoken123", true},
		{"extra spaces", "Bearer   mytoken123  ", "mytoken123", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := extractBearerToken(tt.header)
			if ok != tt.wantOk {
				t.Errorf("extractBearerToken(%q) ok = %v, want %v", tt.header, ok, tt.wantOk)
			}
			if got != tt.want {
				t.Errorf("extractBearerToken(%q) = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}

func TestAuthContextFromGatewayHeaders(t *testing.T) {
	header := http.Header{}
	header.Set("X-User-ID", "user-123")
	header.Set("X-User-Role", "admin")
	header.Set("X-User-Scope", "read write")
	header.Set("X-User-Plan", "pro")
	header.Set("X-User-Plan-Max-File-MB", "500")
	header.Set("X-User-Plan-Max-Files", "50")

	ctx, ok := authContextFromGatewayHeaders(header)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if ctx.UserID != "user-123" {
		t.Errorf("expected UserID 'user-123', got %q", ctx.UserID)
	}
	if ctx.Role != "admin" {
		t.Errorf("expected Role 'admin', got %q", ctx.Role)
	}
	if len(ctx.Scope) != 2 {
		t.Errorf("expected 2 scopes, got %d", len(ctx.Scope))
	}
	if ctx.Plan != "pro" {
		t.Errorf("expected Plan 'pro', got %q", ctx.Plan)
	}
	if ctx.PlanMaxFileSizeMB != 500 {
		t.Errorf("expected PlanMaxFileSizeMB 500, got %d", ctx.PlanMaxFileSizeMB)
	}
	if ctx.PlanMaxFilesPerJob != 50 {
		t.Errorf("expected PlanMaxFilesPerJob 50, got %d", ctx.PlanMaxFilesPerJob)
	}
}

func TestHTTPAuthMiddlewareAnonymousPlanDefaults(t *testing.T) {
	secret := []byte("test-secret-key-32-chars-long!!")
	v, _ := NewVerifier(VerifierConfig{AllowedAlgs: []string{"HS256"}, HMACSecret: secret})

	var gotCtx AuthContext
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authCtx, ok := FromContext(r.Context()); ok {
			gotCtx = authCtx
		}
		w.WriteHeader(http.StatusOK)
	})

	middleware := HTTPAuthMiddleware(HTTPMiddlewareOptions{Verifier: v})
	handler := middleware(next)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotCtx.Plan != "anonymous" {
		t.Errorf("expected anonymous plan for unauthenticated request, got %q", gotCtx.Plan)
	}
	if gotCtx.PlanMaxFileSizeMB != 10 {
		t.Errorf("expected PlanMaxFileSizeMB 10 for anonymous, got %d", gotCtx.PlanMaxFileSizeMB)
	}
	if gotCtx.PlanMaxFilesPerJob != 5 {
		t.Errorf("expected PlanMaxFilesPerJob 5 for anonymous, got %d", gotCtx.PlanMaxFilesPerJob)
	}
}

func TestAuthContextFromGatewayHeadersMissingUserID(t *testing.T) {
	header := http.Header{}
	header.Set("X-User-Role", "admin")

	_, ok := authContextFromGatewayHeaders(header)
	if ok {
		t.Error("expected ok=false when X-User-ID is missing")
	}
}

func TestAuthContextFromGatewayHeadersNil(t *testing.T) {
	_, ok := authContextFromGatewayHeaders(nil)
	if ok {
		t.Error("expected ok=false for nil header")
	}
}

func TestHTTPAuthMiddlewareOptionsPassthrough(t *testing.T) {
	secret := []byte("test-secret-key-32-chars-long!!")
	v, _ := NewVerifier(VerifierConfig{AllowedAlgs: []string{"HS256"}, HMACSecret: secret})

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	middleware := HTTPAuthMiddleware(HTTPMiddlewareOptions{Verifier: v})
	handler := middleware(next)

	req := httptest.NewRequest(http.MethodOptions, "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("expected OPTIONS request to pass through")
	}
}

func TestHTTPAuthMiddlewareValidToken(t *testing.T) {
	secret := []byte("test-secret-key-32-chars-long!!")
	v, _ := NewVerifier(VerifierConfig{AllowedAlgs: []string{"HS256"}, HMACSecret: secret})

	var gotUserID string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authCtx, ok := FromContext(r.Context()); ok {
			gotUserID = authCtx.UserID
		}
		w.WriteHeader(http.StatusOK)
	})

	middleware := HTTPAuthMiddleware(HTTPMiddlewareOptions{Verifier: v})
	handler := middleware(next)

	now := time.Now()
	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-999",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
		},
		Role: "user",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, _ := token.SignedString(secret)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotUserID != "user-999" {
		t.Errorf("expected UserID 'user-999', got %q", gotUserID)
	}
}

func TestHTTPAuthMiddlewareInvalidToken(t *testing.T) {
	secret := []byte("test-secret-key-32-chars-long!!")
	v, _ := NewVerifier(VerifierConfig{AllowedAlgs: []string{"HS256"}, HMACSecret: secret})

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := HTTPAuthMiddleware(HTTPMiddlewareOptions{Verifier: v})
	handler := middleware(next)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected %d, got %d", http.StatusUnauthorized, rec.Code)
	}
}

func TestHTTPAuthMiddlewareNoToken(t *testing.T) {
	secret := []byte("test-secret-key-32-chars-long!!")
	v, _ := NewVerifier(VerifierConfig{AllowedAlgs: []string{"HS256"}, HMACSecret: secret})

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	middleware := HTTPAuthMiddleware(HTTPMiddlewareOptions{Verifier: v})
	handler := middleware(next)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("expected request with no token to pass through (guest access)")
	}
}

func TestSplitScopes(t *testing.T) {
	got := SplitScopes("read write admin")
	if len(got) != 3 {
		t.Errorf("expected 3 scopes, got %d", len(got))
	}
}

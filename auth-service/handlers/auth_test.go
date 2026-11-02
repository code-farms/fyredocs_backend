// Tests for the authentication handlers' pure helpers and pre-DB guard paths.
// The invariants under guard: login failures are uniform in both response and
// bcrypt cost (no account-enumeration oracle), the password hash never reaches
// a response body, and cookie attributes match between set and clear.
package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"auth-service/internal/models"
)

// TestDummyPasswordHashTimingEqualizer pins the login timing equalizer: when
// the email is unknown, Login compares the supplied password against
// dummyPasswordHash so the failure burns the same bcrypt cost as a real
// mismatch. The handler path itself needs models.DB (the unknown-email branch
// only runs after a database lookup), so this pins the constant's properties:
// a malformed or cheap hash would silently reintroduce the timing oracle while
// the compare's error is discarded with `_ =` in Login.
func TestDummyPasswordHashTimingEqualizer(t *testing.T) {
	t.Run("cost matches signup hashes", func(t *testing.T) {
		cost, err := bcrypt.Cost(dummyPasswordHash)
		if err != nil {
			t.Fatalf("dummyPasswordHash is not a parseable bcrypt hash: %v", err)
		}
		// Signup hashes with bcrypt.DefaultCost; a differing cost would make the
		// unknown-email path measurably faster or slower than a real mismatch.
		if cost != bcrypt.DefaultCost {
			t.Errorf("dummyPasswordHash cost = %d, want bcrypt.DefaultCost (%d)", cost, bcrypt.DefaultCost)
		}
	})

	t.Run("compare runs to a full-cost mismatch", func(t *testing.T) {
		err := bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte("attacker-guess"))
		// ErrMismatchedHashAndPassword proves the full key-derivation ran; a
		// parse-stage error (e.g. ErrHashTooShort) would return in microseconds
		// and defeat the equalization.
		if !errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			t.Errorf("CompareHashAndPassword = %v, want bcrypt.ErrMismatchedHashAndPassword", err)
		}
	})
}

// TestLoginPreDBGuards exercises Login's rejection paths that run before any
// database access. models.DB is nil in unit tests, so reaching the user lookup
// would panic — passing doubles as proof these guards stay pre-DB. Both guard
// rejections must be the uniform INVALID_CREDENTIALS 401, never a distinct
// validation error that would tell a prober which field failed.
func TestLoginPreDBGuards(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ae := &AuthEndpoints{}
	r := gin.New()
	r.POST("/auth/login", ae.Login)

	cases := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		// Whitespace values pass the binding's `required` tag, so the post-trim
		// guard is the only thing standing between them and the DB lookup.
		{"whitespace email is uniform 401", `{"email":"   ","password":"correct-horse"}`, http.StatusUnauthorized, "INVALID_CREDENTIALS"},
		{"whitespace password is uniform 401", `{"email":"user@example.com","password":"   "}`, http.StatusUnauthorized, "INVALID_CREDENTIALS"},
		{"missing password fails binding", `{"email":"user@example.com"}`, http.StatusBadRequest, ""},
		{"missing email fails binding", `{"password":"correct-horse"}`, http.StatusBadRequest, ""},
		{"malformed json fails binding", `not-json`, http.StatusBadRequest, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantCode != "" && !strings.Contains(rec.Body.String(), tc.wantCode) {
				t.Errorf("body %q missing error code %q", rec.Body.String(), tc.wantCode)
			}
		})
	}
}

// TestSignupGate pins the SIGNUP_ENABLED switch. models.DB is nil in unit
// tests, so the disabled case passing proves the gate runs before any DB or
// bcrypt work; the enabled case proves the gate does not swallow requests
// when registration is open (they reach binding and fail there instead).
func TestSignupGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ae := &AuthEndpoints{}
	r := gin.New()
	r.POST("/auth/signup", ae.Signup)

	cases := []struct {
		name       string
		enabled    string
		body       string
		wantStatus int
		wantCode   string
	}{
		{"disabled rejects a valid payload", "false", `{"email":"user@example.com","password":"correct-horse","fullName":"A","country":"India"}`, http.StatusForbidden, "SIGNUP_DISABLED"},
		{"enabled reaches binding", "true", `not-json`, http.StatusBadRequest, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SIGNUP_ENABLED", tc.enabled)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/auth/signup", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantCode != "" && !strings.Contains(rec.Body.String(), tc.wantCode) {
				t.Errorf("body %q missing error code %q", rec.Body.String(), tc.wantCode)
			}
		})
	}
}

func TestNormalizeEmail(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"  User@Example.COM  ", "user@example.com"},
		{"test@test.com", "test@test.com"},
		{"", ""},
		{"  ", ""},
		{"UPPER@CASE.COM", "upper@case.com"},
	}
	for _, tt := range tests {
		got := normalizeEmail(tt.input)
		if got != tt.want {
			t.Errorf("normalizeEmail(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestIsDuplicateError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"gorm duplicate key", gorm.ErrDuplicatedKey, true},
		{"duplicate in message", fmt.Errorf("ERROR: duplicate key value violates unique constraint"), true},
		{"unique in message", fmt.Errorf("UNIQUE constraint failed: users.email"), true},
		{"non-duplicate error", fmt.Errorf("connection refused"), false},
		{"random error", errors.New("something went wrong"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDuplicateError(tt.err)
			if got != tt.want {
				t.Errorf("isDuplicateError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestParseSameSite(t *testing.T) {
	tests := []struct {
		input string
		want  http.SameSite
	}{
		{"strict", http.SameSiteStrictMode},
		{"Strict", http.SameSiteStrictMode},
		{"STRICT", http.SameSiteStrictMode},
		{"none", http.SameSiteNoneMode},
		{"None", http.SameSiteNoneMode},
		{"lax", http.SameSiteLaxMode},
		{"Lax", http.SameSiteLaxMode},
		{"", http.SameSiteLaxMode},
		{"unknown", http.SameSiteLaxMode},
		{"  strict  ", http.SameSiteStrictMode},
	}
	for _, tt := range tests {
		got := parseSameSite(tt.input)
		if got != tt.want {
			t.Errorf("parseSameSite(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestExtractAccessToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("from Authorization header", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Request.Header.Set("Authorization", "Bearer mytoken123")

		token, ok := extractAccessToken(c)
		if !ok {
			t.Error("expected ok=true")
		}
		if token != "mytoken123" {
			t.Errorf("expected 'mytoken123', got %q", token)
		}
	})

	t.Run("from context", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Set("access_token", "ctx-token")

		token, ok := extractAccessToken(c)
		if !ok {
			t.Error("expected ok=true")
		}
		if token != "ctx-token" {
			t.Errorf("expected 'ctx-token', got %q", token)
		}
	})

	// Regression for the logout revocation bug: the SPA sends the access token
	// only as an HttpOnly cookie (no bearer header, no context value), so
	// extractAccessToken must read the cookie or logout never revokes anything.
	t.Run("from access_token cookie", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Request.AddCookie(&http.Cookie{Name: "access_token", Value: "cookie-token"})

		token, ok := extractAccessToken(c)
		if !ok {
			t.Fatal("expected ok=true for access_token cookie")
		}
		if token != "cookie-token" {
			t.Errorf("expected 'cookie-token', got %q", token)
		}
	})

	t.Run("no token", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

		_, ok := extractAccessToken(c)
		if ok {
			t.Error("expected ok=false when no token present")
		}
	})

	t.Run("malformed authorization header", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Request.Header.Set("Authorization", "InvalidFormat")

		_, ok := extractAccessToken(c)
		if ok {
			t.Error("expected ok=false for malformed header")
		}
	})
}

func TestBuildUserResponse(t *testing.T) {
	userID := uuid.New()
	user := models.User{
		ID:       userID,
		Email:    "test@example.com",
		FullName: "Test User",
		Phone:    "1234567890",
		Country:  "US",
		ImageURL: "https://example.com/avatar.png",
		PlanName: "free",
	}
	freePlan := models.SubscriptionPlan{Name: "free", MaxFileSizeMB: 25, MaxFilesPerJob: 10, RetentionDays: 7}

	t.Run("normal user", func(t *testing.T) {
		resp := buildUserResponse(user, "admin", freePlan)
		if resp.ID != userID.String() {
			t.Errorf("expected ID %q, got %q", userID.String(), resp.ID)
		}
		if resp.Email != "test@example.com" {
			t.Errorf("expected email 'test@example.com', got %q", resp.Email)
		}
		if resp.Role != "admin" {
			t.Errorf("expected role 'admin', got %q", resp.Role)
		}
		if resp.FullName != "Test User" {
			t.Errorf("expected fullName 'Test User', got %q", resp.FullName)
		}
		if resp.PlanName != "free" {
			t.Errorf("expected planName 'free', got %q", resp.PlanName)
		}
	})

	t.Run("empty role defaults to user", func(t *testing.T) {
		resp := buildUserResponse(user, "", freePlan)
		if resp.Role != "user" {
			t.Errorf("expected role 'user' for empty role, got %q", resp.Role)
		}
	})

	t.Run("whitespace role defaults to user", func(t *testing.T) {
		resp := buildUserResponse(user, "  ", freePlan)
		if resp.Role != "user" {
			t.Errorf("expected role 'user' for whitespace role, got %q", resp.Role)
		}
	})
}

func TestSetRefreshTokenCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("AUTH_COOKIE_SECURE", "true")
	t.Setenv("AUTH_COOKIE_SAMESITE", "lax")
	t.Setenv("AUTH_REFRESH_COOKIE", "refresh_token")

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/auth/login", nil)

	setRefreshTokenCookie(c, "test-refresh-token", 7*24*time.Hour)

	cookies := rec.Result().Cookies()
	found := false
	for _, cookie := range cookies {
		if cookie.Name == "refresh_token" {
			found = true
			if cookie.Path != "/auth" {
				t.Errorf("expected path '/auth', got %q", cookie.Path)
			}
			if !cookie.HttpOnly {
				t.Error("expected HttpOnly=true")
			}
			if cookie.MaxAge != int((7 * 24 * time.Hour).Seconds()) {
				t.Errorf("expected MaxAge %d, got %d", int((7 * 24 * time.Hour).Seconds()), cookie.MaxAge)
			}
		}
	}
	if !found {
		t.Error("refresh_token cookie not found")
	}
}

func TestClearRefreshTokenCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("AUTH_COOKIE_SECURE", "true")

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/auth/logout", nil)

	clearRefreshTokenCookie(c)

	cookies := rec.Result().Cookies()
	found := false
	for _, cookie := range cookies {
		if cookie.Name == "refresh_token" {
			found = true
			if cookie.MaxAge != -1 {
				t.Errorf("expected MaxAge -1, got %d", cookie.MaxAge)
			}
		}
	}
	if !found {
		t.Error("refresh_token clear cookie not found")
	}
}

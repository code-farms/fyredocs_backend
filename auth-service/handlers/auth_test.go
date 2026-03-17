package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"auth-service/internal/models"
)

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

func TestGetEnv(t *testing.T) {
	t.Run("env set", func(t *testing.T) {
		t.Setenv("TEST_ENV_KEY", "value")
		got := getEnv("TEST_ENV_KEY", "fallback")
		if got != "value" {
			t.Errorf("expected 'value', got %q", got)
		}
	})

	t.Run("env empty uses fallback", func(t *testing.T) {
		t.Setenv("TEST_ENV_KEY", "")
		got := getEnv("TEST_ENV_KEY", "fallback")
		if got != "fallback" {
			t.Errorf("expected 'fallback', got %q", got)
		}
	})

	t.Run("env not set uses fallback", func(t *testing.T) {
		got := getEnv("NONEXISTENT_KEY_XYZ", "fallback")
		if got != "fallback" {
			t.Errorf("expected 'fallback', got %q", got)
		}
	})
}

func TestGetEnvBool(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		fallback bool
		want     bool
	}{
		{"true", "true", false, true},
		{"1", "1", false, true},
		{"yes", "yes", false, true},
		{"y", "y", false, true},
		{"false", "false", true, false},
		{"0", "0", true, false},
		{"no", "no", true, false},
		{"n", "n", true, false},
		{"empty fallback true", "", true, true},
		{"empty fallback false", "", false, false},
		{"unknown fallback", "maybe", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TEST_BOOL", tt.value)
			got := getEnvBool("TEST_BOOL", tt.fallback)
			if got != tt.want {
				t.Errorf("getEnvBool(%q, %v) = %v, want %v", tt.value, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestGetEnvDuration(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		fallback time.Duration
		want     time.Duration
	}{
		{"valid duration", "30m", time.Hour, 30 * time.Minute},
		{"valid seconds", "10s", time.Minute, 10 * time.Second},
		{"empty uses fallback", "", 8 * time.Hour, 8 * time.Hour},
		{"invalid uses fallback", "notaduration", 5 * time.Minute, 5 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TEST_DURATION", tt.value)
			got := getEnvDuration("TEST_DURATION", tt.fallback)
			if got != tt.want {
				t.Errorf("getEnvDuration(%q, %v) = %v, want %v", tt.value, tt.fallback, got, tt.want)
			}
		})
	}
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

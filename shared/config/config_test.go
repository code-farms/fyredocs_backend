package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadConfigDoesNotPanic(t *testing.T) {
	// LoadConfig should not panic even without .env file
	LoadConfig()
}

func TestUnquoteValue(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`"hello"`, "hello"},
		{`'hello'`, "hello"},
		{"hello", "hello"},
		{`""`, ""},
		{`''`, ""},
		{"x", "x"},
	}
	for _, tt := range tests {
		result := unquoteValue(tt.input)
		if result != tt.expected {
			t.Errorf("unquoteValue(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestGetEnv(t *testing.T) {
	t.Setenv("TEST_GETENV", "hello")
	if got := GetEnv("TEST_GETENV", "default"); got != "hello" {
		t.Errorf("GetEnv = %q, want %q", got, "hello")
	}
	if got := GetEnv("TEST_GETENV_MISSING", "default"); got != "default" {
		t.Errorf("GetEnv missing = %q, want %q", got, "default")
	}
	t.Setenv("TEST_GETENV_SPACES", "  ")
	if got := GetEnv("TEST_GETENV_SPACES", "default"); got != "default" {
		t.Errorf("GetEnv whitespace = %q, want %q", got, "default")
	}
}

func TestGetEnvBool(t *testing.T) {
	tests := []struct {
		value    string
		fallback bool
		want     bool
	}{
		{"true", false, true},
		{"1", false, true},
		{"yes", false, true},
		{"false", true, false},
		{"0", true, false},
		{"no", true, false},
		{"", false, false},
		{"garbage", true, true},
	}
	for _, tt := range tests {
		t.Setenv("TEST_BOOL", tt.value)
		if got := GetEnvBool("TEST_BOOL", tt.fallback); got != tt.want {
			t.Errorf("GetEnvBool(%q, %v) = %v, want %v", tt.value, tt.fallback, got, tt.want)
		}
	}
}

func TestGetEnvInt(t *testing.T) {
	t.Setenv("TEST_INT", "42")
	if got := GetEnvInt("TEST_INT", 0); got != 42 {
		t.Errorf("GetEnvInt = %d, want 42", got)
	}
	t.Setenv("TEST_INT", "notanumber")
	if got := GetEnvInt("TEST_INT", 10); got != 10 {
		t.Errorf("GetEnvInt invalid = %d, want 10", got)
	}
	if got := GetEnvInt("TEST_INT_MISSING", 5); got != 5 {
		t.Errorf("GetEnvInt missing = %d, want 5", got)
	}
}

func TestGetEnvDuration(t *testing.T) {
	t.Setenv("TEST_DUR", "30s")
	if got := GetEnvDuration("TEST_DUR", time.Minute); got != 30*time.Second {
		t.Errorf("GetEnvDuration = %v, want 30s", got)
	}
	t.Setenv("TEST_DUR", "bad")
	if got := GetEnvDuration("TEST_DUR", time.Minute); got != time.Minute {
		t.Errorf("GetEnvDuration invalid = %v, want 1m", got)
	}
	if got := GetEnvDuration("TEST_DUR_MISSING", 5*time.Second); got != 5*time.Second {
		t.Errorf("GetEnvDuration missing = %v, want 5s", got)
	}
}

func TestTrustedProxies(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "")
	got := TrustedProxies()
	if len(got) != 2 || got[0] != "127.0.0.1" {
		t.Errorf("TrustedProxies empty = %v, want default loopbacks", got)
	}
	t.Setenv("TRUSTED_PROXIES", "10.0.0.1, 10.0.0.2")
	got = TrustedProxies()
	if len(got) != 2 || got[0] != "10.0.0.1" || got[1] != "10.0.0.2" {
		t.Errorf("TrustedProxies = %v, want [10.0.0.1 10.0.0.2]", got)
	}
}

func TestValidateJWTSecret(t *testing.T) {
	t.Setenv("JWT_HS256_SECRET", "")
	t.Setenv("JWT_SECRET", "")
	if err := ValidateJWTSecret(); err == nil {
		t.Error("expected error for empty secret")
	}
	t.Setenv("JWT_HS256_SECRET", "short")
	if err := ValidateJWTSecret(); err == nil {
		t.Error("expected error for short secret")
	}
	t.Setenv("JWT_HS256_SECRET", "change-me")
	if err := ValidateJWTSecret(); err == nil {
		t.Error("expected error for dangerous secret")
	}
	t.Setenv("JWT_HS256_SECRET", "aT9kLmW3xQr7vBn5yHs2jFp8cUe6dGi4")
	if err := ValidateJWTSecret(); err != nil {
		t.Errorf("unexpected error for valid secret: %v", err)
	}
}

func TestNormalizeEnvStripsQuotes(t *testing.T) {
	t.Setenv("TEST_QUOTED_DOUBLE", `"hello"`)
	t.Setenv("TEST_QUOTED_SINGLE", `'world'`)
	normalizeEnv()
	if got := os.Getenv("TEST_QUOTED_DOUBLE"); got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
	if got := os.Getenv("TEST_QUOTED_SINGLE"); got != "world" {
		t.Errorf("expected 'world', got %q", got)
	}
}

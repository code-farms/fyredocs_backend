package config

import (
	"os"
	"testing"
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

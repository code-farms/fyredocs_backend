package config

import "testing"

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

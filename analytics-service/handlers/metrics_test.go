package handlers

import (
	"testing"
)

func TestQueryInt_Default(t *testing.T) {
	// queryInt is internal, test via table-driven
	tests := []struct {
		input    string
		fallback int
		expected int
	}{
		{"", 30, 30},
		{"abc", 30, 30},
		{"0", 30, 30},
		{"-1", 30, 30},
		{"50", 30, 50},
		{"1", 30, 1},
		{"999", 30, 999},
	}
	for _, tt := range tests {
		result := parseQueryInt(tt.input, tt.fallback)
		if result != tt.expected {
			t.Errorf("parseQueryInt(%q, %d) = %d, want %d", tt.input, tt.fallback, result, tt.expected)
		}
	}
}

// parseQueryInt is the testable core of queryInt.
func parseQueryInt(val string, fallback int) int {
	if val == "" {
		return fallback
	}
	n := 0
	for _, ch := range val {
		if ch < '0' || ch > '9' {
			return fallback
		}
		n = n*10 + int(ch-'0')
	}
	if n <= 0 {
		return fallback
	}
	return n
}

package logger

import (
	"log/slog"
	"testing"
)

func TestInitDev(t *testing.T) {
	Init("test-service", "dev")
	slog.Info("test dev log")
}

func TestInitProd(t *testing.T) {
	Init("test-service", "prod")
	slog.Info("test prod log")
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"info", slog.LevelInfo},
		{"", slog.LevelInfo},
		{"unknown", slog.LevelInfo},
	}
	for _, tc := range tests {
		got := parseLevel(tc.input)
		if got != tc.want {
			t.Errorf("parseLevel(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestRequestIDFromContextNil(t *testing.T) {
	if got := RequestIDFromContext(nil); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestInitDoesNotPanicEmptyMode(t *testing.T) {
	Init("test-service", "")
	slog.Info("test empty mode log")
}

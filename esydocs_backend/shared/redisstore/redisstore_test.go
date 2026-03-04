package redisstore

import (
	"testing"
)

func TestGetEnv(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		fallback string
		want     string
	}{
		{"env set", "myvalue", "fallback", "myvalue"},
		{"env empty uses fallback", "", "fallback", "fallback"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TEST_REDIS_ENV", tt.envValue)
			got := getEnv("TEST_REDIS_ENV", tt.fallback)
			if got != tt.want {
				t.Errorf("getEnv = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetEnvInt(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		fallback int
		want     int
	}{
		{"valid int", "5", 0, 5},
		{"empty uses fallback", "", 3, 3},
		{"invalid uses fallback", "abc", 3, 3},
		{"negative", "-1", 0, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TEST_REDIS_INT", tt.envValue)
			got := getEnvInt("TEST_REDIS_INT", tt.fallback)
			if got != tt.want {
				t.Errorf("getEnvInt = %d, want %d", got, tt.want)
			}
		})
	}
}

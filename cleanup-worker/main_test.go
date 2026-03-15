package main

import (
	"testing"
	"time"
)

func TestCleanupInterval(t *testing.T) {
	t.Run("default 15m", func(t *testing.T) {
		t.Setenv("CLEANUP_INTERVAL", "")
		got := cleanupInterval()
		if got != 15*time.Minute {
			t.Errorf("expected 15m, got %v", got)
		}
	})

	t.Run("custom 5m", func(t *testing.T) {
		t.Setenv("CLEANUP_INTERVAL", "5m")
		got := cleanupInterval()
		if got != 5*time.Minute {
			t.Errorf("expected 5m, got %v", got)
		}
	})

	t.Run("invalid uses default", func(t *testing.T) {
		t.Setenv("CLEANUP_INTERVAL", "invalid")
		got := cleanupInterval()
		if got != 15*time.Minute {
			t.Errorf("expected 15m, got %v", got)
		}
	})
}

func TestUploadTTL(t *testing.T) {
	t.Run("default 2h", func(t *testing.T) {
		t.Setenv("UPLOAD_TTL", "")
		got := uploadTTL()
		if got != 2*time.Hour {
			t.Errorf("expected 2h, got %v", got)
		}
	})

	t.Run("custom 30m", func(t *testing.T) {
		t.Setenv("UPLOAD_TTL", "30m")
		got := uploadTTL()
		if got != 30*time.Minute {
			t.Errorf("expected 30m, got %v", got)
		}
	})

	t.Run("invalid uses default", func(t *testing.T) {
		t.Setenv("UPLOAD_TTL", "notaduration")
		got := uploadTTL()
		if got != 2*time.Hour {
			t.Errorf("expected 2h, got %v", got)
		}
	})
}

func TestUploadBaseDir(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv("UPLOAD_DIR", "")
		got := uploadBaseDir()
		if got != "uploads" {
			t.Errorf("expected 'uploads', got %q", got)
		}
	})

	t.Run("custom", func(t *testing.T) {
		t.Setenv("UPLOAD_DIR", "/data/uploads")
		got := uploadBaseDir()
		if got != "/data/uploads" {
			t.Errorf("expected '/data/uploads', got %q", got)
		}
	})
}

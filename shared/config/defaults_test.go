package config

import (
	"testing"
	"time"
)

func TestGuestJobTTL(t *testing.T) {
	t.Setenv("GUEST_JOB_TTL", "")
	if got := GuestJobTTL(); got != 30*time.Minute {
		t.Errorf("default GuestJobTTL = %v, want 30m", got)
	}
	t.Setenv("GUEST_JOB_TTL", "45m")
	if got := GuestJobTTL(); got != 45*time.Minute {
		t.Errorf("GuestJobTTL = %v, want 45m", got)
	}
	t.Setenv("GUEST_JOB_TTL", "not-a-duration")
	if got := GuestJobTTL(); got != 30*time.Minute {
		t.Errorf("invalid GuestJobTTL = %v, want fallback 30m", got)
	}
}

func TestFreeJobTTL(t *testing.T) {
	t.Setenv("FREE_JOB_TTL", "")
	if got := FreeJobTTL(); got != 7*24*time.Hour {
		t.Errorf("default FreeJobTTL = %v, want 168h", got)
	}
	t.Setenv("FREE_JOB_TTL", "72h")
	if got := FreeJobTTL(); got != 72*time.Hour {
		t.Errorf("FreeJobTTL = %v, want 72h", got)
	}
	t.Setenv("FREE_JOB_TTL", "bogus")
	if got := FreeJobTTL(); got != 7*24*time.Hour {
		t.Errorf("invalid FreeJobTTL = %v, want fallback 168h", got)
	}
}

func TestProJobTTL(t *testing.T) {
	t.Setenv("PRO_JOB_TTL", "")
	if got := ProJobTTL(); got != 30*24*time.Hour {
		t.Errorf("default ProJobTTL = %v, want 720h", got)
	}
	t.Setenv("PRO_JOB_TTL", "1440h")
	if got := ProJobTTL(); got != 1440*time.Hour {
		t.Errorf("ProJobTTL = %v, want 1440h", got)
	}
}

func TestUploadTTL(t *testing.T) {
	t.Setenv("UPLOAD_TTL", "")
	if got := UploadTTL(); got != 30*time.Minute {
		t.Errorf("default UploadTTL = %v, want 30m", got)
	}
	t.Setenv("UPLOAD_TTL", "1h")
	if got := UploadTTL(); got != time.Hour {
		t.Errorf("UploadTTL = %v, want 1h", got)
	}
	t.Setenv("UPLOAD_TTL", "junk")
	if got := UploadTTL(); got != 30*time.Minute {
		t.Errorf("invalid UploadTTL = %v, want fallback 30m", got)
	}
}

func TestCleanupInterval(t *testing.T) {
	t.Setenv("CLEANUP_INTERVAL", "")
	if got := CleanupInterval(); got != 15*time.Minute {
		t.Errorf("default CleanupInterval = %v, want 15m", got)
	}
	t.Setenv("CLEANUP_INTERVAL", "5m")
	if got := CleanupInterval(); got != 5*time.Minute {
		t.Errorf("CleanupInterval = %v, want 5m", got)
	}
	t.Setenv("CLEANUP_INTERVAL", "oops")
	if got := CleanupInterval(); got != 15*time.Minute {
		t.Errorf("invalid CleanupInterval = %v, want fallback 15m", got)
	}
}

func TestStaleMultipartAge(t *testing.T) {
	t.Setenv("MULTIPART_ABORT_TTL", "")
	if got := StaleMultipartAge(); got != 24*time.Hour {
		t.Errorf("default StaleMultipartAge = %v, want 24h", got)
	}
	t.Setenv("MULTIPART_ABORT_TTL", "48h")
	if got := StaleMultipartAge(); got != 48*time.Hour {
		t.Errorf("StaleMultipartAge = %v, want 48h", got)
	}
}

func TestMaxUploadBytes(t *testing.T) {
	t.Setenv("MAX_UPLOAD_MB", "")
	if got := MaxUploadBytes(); got != 500*1024*1024 {
		t.Errorf("default MaxUploadBytes = %d, want 500MiB", got)
	}
	t.Setenv("MAX_UPLOAD_MB", "100")
	if got := MaxUploadBytes(); got != 100*1024*1024 {
		t.Errorf("MaxUploadBytes = %d, want 100MiB", got)
	}
	t.Setenv("MAX_UPLOAD_MB", "-5")
	if got := MaxUploadBytes(); got != 500*1024*1024 {
		t.Errorf("negative MaxUploadBytes = %d, want fallback 500MiB", got)
	}
	t.Setenv("MAX_UPLOAD_MB", "abc")
	if got := MaxUploadBytes(); got != 500*1024*1024 {
		t.Errorf("invalid MaxUploadBytes = %d, want fallback 500MiB", got)
	}
}

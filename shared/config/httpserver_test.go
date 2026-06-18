package config

import (
	"net/http"
	"testing"
	"time"
)

func TestApplyServerTimeoutsNonStreaming(t *testing.T) {
	srv := &http.Server{}
	ApplyServerTimeouts(srv, false)

	if srv.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want 10s", srv.ReadHeaderTimeout)
	}
	if srv.ReadTimeout != 30*time.Second {
		t.Errorf("ReadTimeout = %v, want 30s", srv.ReadTimeout)
	}
	if srv.IdleTimeout != 120*time.Second {
		t.Errorf("IdleTimeout = %v, want 120s", srv.IdleTimeout)
	}
	if srv.WriteTimeout != 60*time.Second {
		t.Errorf("WriteTimeout = %v, want 60s for non-streaming", srv.WriteTimeout)
	}
}

func TestApplyServerTimeoutsStreamingLeavesWriteUnset(t *testing.T) {
	srv := &http.Server{}
	ApplyServerTimeouts(srv, true)

	if srv.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want 10s", srv.ReadHeaderTimeout)
	}
	if srv.WriteTimeout != 0 {
		t.Errorf("WriteTimeout = %v, want 0 (unlimited) for streaming services", srv.WriteTimeout)
	}
}

func TestApplyServerTimeoutsRespectsEnvOverride(t *testing.T) {
	t.Setenv("HTTP_WRITE_TIMEOUT", "5s")
	t.Setenv("HTTP_IDLE_TIMEOUT", "45s")
	srv := &http.Server{}
	ApplyServerTimeouts(srv, false)

	if srv.WriteTimeout != 5*time.Second {
		t.Errorf("WriteTimeout = %v, want 5s from env", srv.WriteTimeout)
	}
	if srv.IdleTimeout != 45*time.Second {
		t.Errorf("IdleTimeout = %v, want 45s from env", srv.IdleTimeout)
	}
}

func TestApplyServerTimeoutsNilSafe(t *testing.T) {
	ApplyServerTimeouts(nil, false) // must not panic
}

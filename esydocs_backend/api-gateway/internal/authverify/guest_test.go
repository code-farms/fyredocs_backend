package authverify

import (
	"testing"
)

func TestNewRedisGuestStoreNilClient(t *testing.T) {
	store := NewRedisGuestStore(nil, GuestStoreConfig{})
	if store != nil {
		t.Error("expected nil store for nil client")
	}
}

func TestNewRedisTokenDenylistNilClient(t *testing.T) {
	d := NewRedisTokenDenylist(nil, "")
	if d != nil {
		t.Error("expected nil denylist for nil client")
	}
}

func TestGetEnvInt(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		fallback int
		want     int
	}{
		{"valid int", "42", 0, 42},
		{"empty uses fallback", "", 10, 10},
		{"invalid uses fallback", "abc", 10, 10},
		{"negative", "-5", 0, -5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TEST_INT", tt.envValue)
			got := getEnvInt("TEST_INT", tt.fallback)
			if got != tt.want {
				t.Errorf("getEnvInt = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestDenylistNilReceiverSafe(t *testing.T) {
	var d *RedisTokenDenylist
	denied, err := d.IsTokenDenied(nil, "token")
	if denied || err != nil {
		t.Errorf("expected false/nil for nil receiver, got %v/%v", denied, err)
	}
	err = d.DenyToken(nil, "token", 0)
	if err != nil {
		t.Errorf("expected nil for nil receiver, got %v", err)
	}
}

func TestGuestStoreNilReceiverSafe(t *testing.T) {
	var s *RedisGuestStore
	valid, err := s.ValidateGuestToken(nil, "token")
	if valid || err != nil {
		t.Errorf("expected false/nil for nil receiver, got %v/%v", valid, err)
	}
}

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

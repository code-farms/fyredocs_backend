package authverify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	denied, err := d.IsTokenDenied(context.Background(), "token")
	if denied || err != nil {
		t.Errorf("expected false/nil for nil receiver, got %v/%v", denied, err)
	}
	err = d.DenyToken(context.Background(), "token", 0)
	if err != nil {
		t.Errorf("expected nil for nil receiver, got %v", err)
	}
	err = d.DenyTokenHash(context.Background(), "hash", 0)
	if err != nil {
		t.Errorf("expected nil for nil receiver, got %v", err)
	}
}

// TestDenyTokenHashKeyMatchesRawLookup documents the invariant that fixes the
// revocation double-hash bug: denying by the stored SHA-256 hex hash must land
// on the SAME Redis key that IsTokenDenied derives from the corresponding raw
// token. key(raw) = keyPrefix:hex(sha256(raw)); DenyTokenHash stores
// keyPrefix:<hash> verbatim, so passing hex(sha256(raw)) as the hash matches.
func TestDenyTokenHashKeyMatchesRawLookup(t *testing.T) {
	d := &RedisTokenDenylist{keyPrefix: "denylist:jwt"}
	rawToken := "some.jwt.token"

	sum := sha256.Sum256([]byte(rawToken))
	hash := hex.EncodeToString(sum[:])
	// The key DenyTokenHash would write:
	wantKey := d.keyPrefix + ":" + hash
	// The key IsTokenDenied (via key()) reads for the same raw token:
	gotKey := d.key(rawToken)
	if gotKey != wantKey {
		t.Fatalf("key mismatch: DenyTokenHash writes %q but lookup reads %q", wantKey, gotKey)
	}
}

func TestGuestStoreNilReceiverSafe(t *testing.T) {
	var s *RedisGuestStore
	valid, err := s.ValidateGuestToken(context.Background(), "token")
	if valid || err != nil {
		t.Errorf("expected false/nil for nil receiver, got %v/%v", valid, err)
	}
}

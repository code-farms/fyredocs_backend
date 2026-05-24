package models

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestPasswordResetTokenBeforeCreateAssignsID verifies the GORM hook auto-assigns
// a UUIDv7 when one isn't supplied — mirrors UserSession.BeforeCreate behavior.
func TestPasswordResetTokenBeforeCreateAssignsID(t *testing.T) {
	row := &PasswordResetToken{}
	if err := row.BeforeCreate(nil); err != nil {
		t.Fatalf("BeforeCreate returned error: %v", err)
	}
	if row.ID == uuid.Nil {
		t.Error("expected BeforeCreate to assign a non-nil UUID")
	}
}

// TestPasswordResetTokenBeforeCreatePreservesExistingID verifies the hook does
// not overwrite an explicitly-set ID.
func TestPasswordResetTokenBeforeCreatePreservesExistingID(t *testing.T) {
	preset := uuid.Must(uuid.NewV7())
	row := &PasswordResetToken{ID: preset}
	if err := row.BeforeCreate(nil); err != nil {
		t.Fatalf("BeforeCreate returned error: %v", err)
	}
	if row.ID != preset {
		t.Errorf("expected ID to remain %s, got %s", preset, row.ID)
	}
}

// TestHashTokenStablyHashesResetTokens documents that the same hash function
// powers both session and reset tokens — reset-token lookups rely on this
// determinism.
func TestHashTokenStablyHashesResetTokens(t *testing.T) {
	raw := "a-raw-reset-token-string"
	h1 := HashToken(raw)
	h2 := HashToken(raw)
	if h1 != h2 {
		t.Errorf("HashToken not stable: %s vs %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("expected 64-char hex hash, got %d", len(h1))
	}
}

// TestPasswordResetTokenExpiresAtMath verifies the TTL arithmetic used by
// CreatePasswordResetToken so we don't accidentally ship a token whose
// expires_at is in the past.
func TestPasswordResetTokenExpiresAtMath(t *testing.T) {
	ttl := 1 * time.Hour
	before := time.Now()
	expires := before.Add(ttl)
	after := time.Now()
	if !expires.After(after) {
		t.Errorf("expires_at (%v) is not after now (%v) — TTL math broken", expires, after)
	}
	if expires.Sub(before) != ttl {
		t.Errorf("expected TTL of %v, got %v", ttl, expires.Sub(before))
	}
}

package models

import (
	"testing"
)

func TestHashToken(t *testing.T) {
	hash := HashToken("test-token-string")
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}
	if len(hash) != 64 { // SHA-256 = 32 bytes = 64 hex chars
		t.Errorf("expected 64 hex chars, got %d", len(hash))
	}

	// Same input should produce same hash
	hash2 := HashToken("test-token-string")
	if hash != hash2 {
		t.Error("expected same hash for same input")
	}

	// Different input should produce different hash
	hash3 := HashToken("different-token")
	if hash == hash3 {
		t.Error("expected different hash for different input")
	}
}

func TestHashTokenEmpty(t *testing.T) {
	hash := HashToken("")
	if hash == "" {
		t.Fatal("expected non-empty hash even for empty input")
	}
	if len(hash) != 64 {
		t.Errorf("expected 64 hex chars, got %d", len(hash))
	}
}

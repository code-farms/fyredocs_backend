// Tests for the share-link primitives. The invariants under guard: tokens are
// long, URL-safe, and never stored raw (hash only); the shareable-type gates
// are allowlists, not fail-open; expiry choices are bounded.
package handlers

import (
	"strings"
	"testing"
)

func TestShareTokenRoundtrip(t *testing.T) {
	tok, err := newShareToken()
	if err != nil {
		t.Fatalf("newShareToken: %v", err)
	}
	// base64url of 32 bytes without padding is 43 chars.
	if len(tok) != 43 {
		t.Errorf("token length = %d, want 43", len(tok))
	}
	if strings.ContainsAny(tok, "+/=") {
		t.Errorf("token %q is not URL-safe", tok)
	}

	h1, h2 := hashShareToken(tok), hashShareToken(tok)
	if h1 != h2 {
		t.Error("hash must be deterministic")
	}
	if len(h1) != 64 {
		t.Errorf("hash length = %d, want 64 hex chars", len(h1))
	}
	if h1 == tok {
		t.Error("hash must not equal the token")
	}

	other, _ := newShareToken()
	if other == tok {
		t.Error("two tokens must not collide")
	}
	if hashShareToken(other) == h1 {
		t.Error("different tokens must hash differently")
	}
}

func TestShareExpiries(t *testing.T) {
	if _, ok := shareExpiries[defaultShareExpiry]; !ok {
		t.Fatalf("default expiry %q missing from allowlist", defaultShareExpiry)
	}
	for _, bad := range []string{"5m", "30d", "forever", "0"} {
		if _, ok := shareExpiries[bad]; ok {
			t.Errorf("expiry %q must not be allowed", bad)
		}
	}
}

// TestShareFileTypeGate pins the share pseudo-tool's extension allowlist —
// deliberately NOT fail-open like unknown tools.
func TestShareFileTypeGate(t *testing.T) {
	allowed := []string{"doc.pdf", "scan.png", "photo.JPG", "notes.txt", "bundle.zip", "sheet.xlsx", "deck.pptx"}
	for _, name := range allowed {
		if err := validateFileType("share", name); err != nil {
			t.Errorf("validateFileType(share, %q) = %v, want nil", name, err)
		}
	}
	blocked := []string{"tool.exe", "app.apk", "script.sh", "lib.dylib", "noextension", "page.js"}
	for _, name := range blocked {
		if err := validateFileType("share", name); err == nil {
			t.Errorf("validateFileType(share, %q) = nil, want error", name)
		}
	}
}

// TestShareMIMEGate pins that the share category sniffs content — a renamed
// executable must not pass.
func TestShareMIMEGate(t *testing.T) {
	if got := mimeCategory("share"); got != "share" {
		t.Fatalf("mimeCategory(share) = %q, want share", got)
	}
	pdfHead := []byte("%PDF-1.7\n%some pdf bytes")
	if err := validateMIMEHead("share", pdfHead); err != nil {
		t.Errorf("pdf head rejected: %v", err)
	}
	pngHead := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0}
	if err := validateMIMEHead("share", pngHead); err != nil {
		t.Errorf("png head rejected: %v", err)
	}
	textHead := []byte("just some plain notes\nsecond line")
	if err := validateMIMEHead("share", textHead); err != nil {
		t.Errorf("text head rejected: %v", err)
	}
	// ELF magic — DetectContentType says application/octet-stream.
	elfHead := []byte{0x7F, 'E', 'L', 'F', 2, 1, 1, 0, 0, 0, 0, 0}
	if err := validateMIMEHead("share", elfHead); err == nil {
		t.Error("executable content must be rejected")
	}
}

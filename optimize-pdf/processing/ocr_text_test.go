package processing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// writeTxt writes a page sidecar and returns its path.
func writeTxt(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

func TestCollectOCRTextOrderAndPageBreaks(t *testing.T) {
	dir := t.TempDir()
	p1 := writeTxt(t, dir, "a.txt", "page one\n\n")
	p2 := writeTxt(t, dir, "b.txt", "page two\f")

	text, truncated := collectOCRText([]string{p1, p2}, ocrTextCapBytes)
	if truncated {
		t.Fatal("unexpected truncation")
	}
	if text != "page one\fpage two" {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestCollectOCRTextFallbackPagesKeepPositions(t *testing.T) {
	dir := t.TempDir()
	p1 := writeTxt(t, dir, "a.txt", "first")
	p3 := writeTxt(t, dir, "c.txt", "third")

	// Middle page failed OCR → empty slot, but the page break must survive so
	// text positions still line up with the merged PDF.
	text, _ := collectOCRText([]string{p1, "", p3}, ocrTextCapBytes)
	if text != "first\f\fthird" {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestCollectOCRTextAllEmpty(t *testing.T) {
	dir := t.TempDir()
	blank := writeTxt(t, dir, "blank.txt", "  \n\f")

	if text, truncated := collectOCRText([]string{"", blank}, ocrTextCapBytes); text != "" || truncated {
		t.Fatalf("expected empty result, got %q (truncated=%v)", text, truncated)
	}
	if text, _ := collectOCRText(nil, ocrTextCapBytes); text != "" {
		t.Fatalf("nil input should yield empty text, got %q", text)
	}
}

func TestCollectOCRTextMissingFileSkipped(t *testing.T) {
	dir := t.TempDir()
	p1 := writeTxt(t, dir, "a.txt", "kept")

	text, _ := collectOCRText([]string{p1, filepath.Join(dir, "vanished.txt")}, ocrTextCapBytes)
	if text != "kept\f" {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestCollectOCRTextTruncationIsRuneSafe(t *testing.T) {
	dir := t.TempDir()
	// Multi-byte runes across the cap boundary: é is 2 bytes in UTF-8.
	p := writeTxt(t, dir, "a.txt", strings.Repeat("é", 100))

	text, truncated := collectOCRText([]string{p}, 33)
	if !truncated {
		t.Fatal("expected truncation")
	}
	if len(text) > 33 {
		t.Fatalf("cap exceeded: %d bytes", len(text))
	}
	if !utf8.ValidString(text) {
		t.Fatal("truncation split a rune")
	}
	if text != strings.Repeat("é", 16) {
		t.Fatalf("unexpected truncated text: %q", text)
	}
}

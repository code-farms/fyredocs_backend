// Tests the OCR language allowlist — a trust boundary, not a preference check. The
// requested language reaches a tesseract command line, so anything off the allowlist
// must be coerced to the default rather than passed through (finding I1 —
// docs/developer/architecture/backend-hardening.md section 3.20).
package processing

import (
	"strings"
	"testing"
)

// TestSanitizeOCRLanguage verifies the OCR language trust boundary (finding I1):
// only whitelisted tesseract codes survive; ISO 639-1 codes are mapped; anything
// unmapped, crafted, or empty falls back to "eng" and never reaches `tesseract -l`
// verbatim.
func TestSanitizeOCRLanguage(t *testing.T) {
	cases := map[string]string{
		"":                         "eng", // empty → default
		"en":                       "eng", // ISO 639-1 mapped
		"de":                       "deu",
		"zh":                       "chi_sim",
		"eng":                      "eng", // already a tesseract code
		"chi_sim":                  "chi_sim",
		"  fr  ":                   "fra", // trimmed then mapped
		"xx":                       "eng", // unknown code → default
		"eng; rm -rf /":            "eng", // crafted value rejected
		"../../usr/share/tessdata": "eng", // path-like value rejected
		"ENG":                      "eng", // case-sensitive: unknown → default
	}
	for in, want := range cases {
		if got := sanitizeOCRLanguage(in); got != want {
			t.Errorf("sanitizeOCRLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSanitizeOCRLanguageAdversarial widens the hostile-input surface beyond
// the rows above: shell metacharacters, control bytes, tesseract's own
// multi-language syntax, oversized values, and unicode homoglyphs. Whatever
// goes in, the output must be a member of ocrAllowedLanguages — the invariant
// check at the bottom is the real guard; the exact-value rows document the
// current mapping.
func TestSanitizeOCRLanguageAdversarial(t *testing.T) {
	cases := map[string]string{
		// Shell/command injection shapes: never passed through.
		"eng$(reboot)":  "eng",
		"eng`id`":       "eng",
		"eng && whoami": "eng",
		"-l eng":        "eng",
		// Control bytes: TrimSpace only strips the edges, so an interior
		// newline or NUL makes the value unrecognized → default.
		"eng\ndeu": "eng",
		"eng\x00":  "eng",
		// Leading/trailing whitespace (incl. newline) is trimmed, so a legit
		// code survives sloppy client serialization.
		"\neng\t": "eng",
		" de \n":  "deu",
		// Tesseract multi-language syntax is NOT supported: pinned — "eng+deu"
		// is off the allowlist and coerces to the default rather than passing
		// a combo through to `tesseract -l`.
		"eng+deu": "eng",
		"deu+eng": "eng",
		// Unicode homoglyphs (Cyrillic е U+0435, fullwidth letters) must not
		// match: the output is the ASCII default, never the homoglyph itself.
		"еng":                      "eng",
		"ｅｎｇ":                      "eng",
		strings.Repeat("a", 100):   "eng",
		strings.Repeat("eng", 100): "eng",
	}
	for in, want := range cases {
		got := sanitizeOCRLanguage(in)
		if got != want {
			t.Errorf("sanitizeOCRLanguage(%q) = %q, want %q", in, got, want)
		}
		if !ocrAllowedLanguages[got] {
			t.Errorf("sanitizeOCRLanguage(%q) = %q escaped the allowlist — this value would reach `tesseract -l` verbatim", in, got)
		}
	}
}

// TestSanitizeOCRLanguageAllowlistClosed verifies every allowlisted code (and
// every ISO 639-1 alias) round-trips to an allowlisted code, so extending
// ocrLangMap without extending ocrAllowedLanguages fails here instead of at a
// tesseract invocation.
func TestSanitizeOCRLanguageAllowlistClosed(t *testing.T) {
	for code := range ocrAllowedLanguages {
		if got := sanitizeOCRLanguage(code); got != code {
			t.Errorf("allowlisted code %q must pass through unchanged, got %q", code, got)
		}
	}
	for iso, tess := range ocrLangMap {
		got := sanitizeOCRLanguage(iso)
		if got != tess {
			t.Errorf("sanitizeOCRLanguage(%q) = %q, want mapped %q", iso, got, tess)
		}
		if !ocrAllowedLanguages[got] {
			t.Errorf("ocrLangMap[%q] = %q is not in ocrAllowedLanguages — the two tables drifted", iso, tess)
		}
	}
}

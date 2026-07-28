// Tests the OCR language allowlist — a trust boundary, not a preference check. The
// requested language reaches a tesseract command line, so anything off the allowlist
// must be coerced to the default rather than passed through (finding I1 —
// docs/developer/architecture/backend-hardening.md section 3.20).
package processing

import "testing"

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

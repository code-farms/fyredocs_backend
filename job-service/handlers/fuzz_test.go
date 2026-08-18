// Fuzz targets for the upload-protocol parsers — the two places raw client
// strings shape object keys and S3 part lists. Invariants: no panic, no path
// separator or dot-dot ever survives sanitization, and part numbers stay
// inside the announced range.
package handlers

import (
	"strconv"
	"strings"
	"testing"
)

func FuzzSanitizeFileName(f *testing.F) {
	for _, seed := range []string{
		"report.pdf", "  padded.pdf  ", "../../etc/passwd", "/abs/path/file.pdf",
		"..", ".", "", "a/b/c.pdf", "..\\..\\win.pdf", "nul\x00byte.pdf",
		"．．/fullwidth.pdf", strings.Repeat("x", 300) + ".pdf",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, name string) {
		out := sanitizeFileName(name)
		if out == "" {
			return
		}
		// The output becomes the tail of an object key: a surviving separator
		// would nest (or escape) key prefixes, and a bare ".." re-resolves at
		// any dot-normalizing hop.
		if strings.ContainsRune(out, '/') {
			t.Errorf("sanitizeFileName(%q) = %q — contains a path separator", name, out)
		}
		if out == ".." || out == "." {
			t.Errorf("sanitizeFileName(%q) = %q — dot segment survived", name, out)
		}
		if len(out) > len(name) {
			t.Errorf("sanitizeFileName(%q) grew the name: %q", name, out)
		}
	})
}

func FuzzParsePartNumbers(f *testing.F) {
	f.Add("1,2,3", 5)
	f.Add("", 3)
	f.Add("0", 3)
	f.Add("4", 3)
	f.Add("1,1", 3)
	f.Add("-1,2", 3)
	f.Add("1, 2 ,3", 3)
	f.Add("999999999999999999999", 3)
	f.Add("1;2", 3)
	f.Fuzz(func(t *testing.T, raw string, totalParts int) {
		// totalParts is server-derived (ceil(size/partSize), capped at 1000);
		// the clamp keeps the fuzzer inside realizable inputs.
		if totalParts < 0 {
			totalParts = -totalParts
		}
		totalParts = totalParts%1000 + 1

		nums, err := parsePartNumbers(raw, totalParts)
		if err != nil {
			return
		}
		for _, n := range nums {
			if n < 1 || n > totalParts {
				t.Errorf("parsePartNumbers(%q, %d) accepted out-of-range part %d", raw, totalParts, n)
			}
		}
		// Every accepted token must round-trip as a plain integer — anything
		// else means a malformed token slipped through the strict parse.
		for _, tok := range strings.Split(raw, ",") {
			if _, convErr := strconv.Atoi(strings.TrimSpace(tok)); convErr != nil && len(nums) > 0 && strings.TrimSpace(raw) != "" {
				// A single bad token must fail the whole parse (err above),
				// so reaching here with results means the contract broke.
				t.Errorf("parsePartNumbers(%q, %d) returned %v despite malformed token %q", raw, totalParts, nums, tok)
				break
			}
		}
	})
}

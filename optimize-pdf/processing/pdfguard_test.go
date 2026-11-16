package processing

import (
	"math"
	"testing"
)

func TestCharErrorRate(t *testing.T) {
	cases := []struct {
		name      string
		want, got string
		cer       float64
	}{
		{"identical", "hello world", "hello world", 0},
		{"one substitution", "abcd", "abxd", 0.25},
		{"empty both", "", "", 0},
		{"empty reference", "", "x", 1},
		{"everything lost", "abcd", "", 1},
		// The Devanagari breakage: the output text is a short, wrong string.
		{"devanagari broken", "यह फायरडॉक्स का हिंदी परीक्षण दस्तावेज़ है", "फा फा फा", 0.85},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := charErrorRate(c.want, c.got)
			if c.name == "devanagari broken" {
				if got <= textGuardMaxCER {
					t.Fatalf("CER %.3f must exceed the guard threshold %.2f", got, textGuardMaxCER)
				}
				return
			}
			if math.Abs(got-c.cer) > 1e-9 {
				t.Fatalf("CER = %v, want %v", got, c.cer)
			}
		})
	}
}

func TestCharErrorRateCapsInput(t *testing.T) {
	long := make([]rune, textGuardMaxRunes*3)
	for i := range long {
		long[i] = 'a'
	}
	// Equal beyond the cap: compared only up to it, so 0 and fast.
	if got := charErrorRate(string(long), string(long[:textGuardMaxRunes])); got != 0 {
		t.Fatalf("CER over capped input = %v, want 0", got)
	}
}

func TestSortPageFiles(t *testing.T) {
	files := []string{"/x/page-10.pdf", "/x/page-2.pdf", "/x/page-1.pdf"}
	sortPageFiles(files)
	want := []string{"/x/page-1.pdf", "/x/page-2.pdf", "/x/page-10.pdf"}
	for i := range want {
		if files[i] != want[i] {
			t.Fatalf("order %v, want %v", files, want)
		}
	}
}

func TestAddWarningAppends(t *testing.T) {
	meta := map[string]interface{}{}
	addWarning(meta, "a")
	addWarning(meta, "b")
	ws, _ := meta["warnings"].([]JobWarning)
	if len(ws) != 2 || ws[0].Code != "a" || ws[1].Code != "b" || ws[0].Page != 0 {
		t.Fatalf("warnings = %v", meta["warnings"])
	}
}

func TestOCRTesseractArgs(t *testing.T) {
	got := ocrTesseractArgs("p.png", "out", "eng", 150, true)
	want := []string{"p.png", "out", "-l", "eng", "--dpi", "150", "-c", "textonly_pdf=1", "pdf", "txt"}
	if len(got) != len(want) {
		t.Fatalf("overlay args %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("overlay args %v, want %v", got, want)
		}
	}
	// Rebuild mode keeps the image in tesseract's PDF.
	for _, a := range ocrTesseractArgs("p.png", "out", "eng", 150, false) {
		if a == "textonly_pdf=1" || a == "--dpi" {
			t.Fatalf("rebuild mode must not ask for a text-only PDF: %v", a)
		}
	}
}

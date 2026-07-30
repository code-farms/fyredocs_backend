package processing

import "testing"

// TestParsePdfToImageOptions pins the format allowlist (a trust boundary —
// the flag reaches the pdftoppm command line) and the DPI clamp defaults.
func TestParsePdfToImageOptions(t *testing.T) {
	tests := []struct {
		name     string
		options  map[string]interface{}
		wantFlag string
		wantExt  string
		wantDPI  int
	}{
		{"nil options", nil, "-png", ".png", 150},
		{"empty options", map[string]interface{}{}, "-png", ".png", 150},
		{"png", map[string]interface{}{"format": "png"}, "-png", ".png", 150},
		{"jpg", map[string]interface{}{"format": "jpg"}, "-jpeg", ".jpg", 150},
		{"jpeg alias", map[string]interface{}{"format": "JPEG"}, "-jpeg", ".jpg", 150},
		{"tiff", map[string]interface{}{"format": "tiff"}, "-tiff", ".tif", 150},
		{"unknown degrades to png", map[string]interface{}{"format": "bmp"}, "-png", ".png", 150},
		{"crafted degrades to png", map[string]interface{}{"format": "png -r 9999"}, "-png", ".png", 150},
		{"dpi string", map[string]interface{}{"dpi": "300"}, "-png", ".png", 300},
		{"dpi number", map[string]interface{}{"dpi": 300}, "-png", ".png", 300},
		{"dpi clamped low", map[string]interface{}{"dpi": "10"}, "-png", ".png", 72},
		{"dpi clamped high", map[string]interface{}{"dpi": 5000}, "-png", ".png", 600},
		{"dpi garbage keeps default", map[string]interface{}{"dpi": "lots"}, "-png", ".png", 150},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flag, ext, dpi := parsePdfToImageOptions(tt.options)
			if flag != tt.wantFlag || ext != tt.wantExt || dpi != tt.wantDPI {
				t.Errorf("parsePdfToImageOptions(%v) = (%q, %q, %d), want (%q, %q, %d)",
					tt.options, flag, ext, dpi, tt.wantFlag, tt.wantExt, tt.wantDPI)
			}
		})
	}
}

// TestComputeSafeDPIImage mirrors optimize-pdf's clamp semantics (duplicated
// helper — keep behavior in sync).
func TestComputeSafeDPIImage(t *testing.T) {
	// A4 (595x842 pts) at 300 DPI → ~3508 px, under a 10000 cap: unchanged.
	if got := computeSafeDPI(595, 842, 300, 10000); got != 300 {
		t.Errorf("A4@300 = %d, want 300", got)
	}
	// A huge page must clamp down: 72000 pts (1000in) at 300 DPI → 300000 px.
	if got := computeSafeDPI(72000, 100, 300, 10000); got != 10 {
		t.Errorf("huge page = %d, want 10", got)
	}
	// Unknown dimensions pass through.
	if got := computeSafeDPI(0, 0, 300, 10000); got != 300 {
		t.Errorf("unknown dims = %d, want 300", got)
	}
}

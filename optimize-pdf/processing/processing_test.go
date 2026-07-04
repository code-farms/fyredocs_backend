package processing

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestProcessFileNoInputs(t *testing.T) {
	_, err := ProcessFile(context.Background(), uuid.New(), "compress-pdf", nil, nil, "")
	if err == nil {
		t.Error("expected error for no input files")
	}
}

func TestOCRMaxWorkers(t *testing.T) {
	// Explicit override is honored.
	t.Setenv("OCR_MAX_WORKERS", "8")
	if got := ocrMaxWorkers(); got != 8 {
		t.Errorf("OCR_MAX_WORKERS=8 → %d, want 8", got)
	}
	// Values below 1 clamp to 1.
	t.Setenv("OCR_MAX_WORKERS", "0")
	if got := ocrMaxWorkers(); got != 1 {
		t.Errorf("OCR_MAX_WORKERS=0 → %d, want 1", got)
	}
	// Unset/invalid falls back to a CPU-sized pool capped at 4.
	t.Setenv("OCR_MAX_WORKERS", "")
	if got := ocrMaxWorkers(); got < 1 || got > 4 {
		t.Errorf("default OCR workers = %d, want 1..4", got)
	}
	t.Setenv("OCR_MAX_WORKERS", "notnum")
	if got := ocrMaxWorkers(); got < 1 || got > 4 {
		t.Errorf("invalid OCR_MAX_WORKERS → %d, want fallback 1..4", got)
	}
}

func TestComputeSafeDPI(t *testing.T) {
	tests := []struct {
		name      string
		w, h      float64
		requested int
		maxDim    int
		want      int
	}{
		// Letter page (612x792 pts) at 300 DPI → 3300px longest, under 10000 cap.
		{"small page keeps requested", 612, 792, 300, 10000, 300},
		// Huge banner page: 20000pt longest at 300 DPI → ~83000px, far over cap.
		// safe = floor(10000*72/20000) = 36.
		{"huge page capped", 20000, 5000, 300, 10000, 36},
		// Requested already below the fit DPI → unchanged.
		{"requested below cap unchanged", 5000, 5000, 72, 10000, 72},
		// Unknown dimensions → requested unchanged.
		{"zero dims unchanged", 0, 0, 300, 10000, 300},
		// Non-positive maxDim → requested unchanged.
		{"no cap unchanged", 20000, 5000, 300, 0, 300},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := computeSafeDPI(tt.w, tt.h, tt.requested, tt.maxDim); got != tt.want {
				t.Errorf("computeSafeDPI(%v,%v,%d,%d) = %d, want %d", tt.w, tt.h, tt.requested, tt.maxDim, got, tt.want)
			}
		})
	}

	// A capped result must keep the longest edge within maxDim pixels.
	t.Run("cap keeps edge within bound", func(t *testing.T) {
		w, h, requested, maxDim := 20000.0, 5000.0, 300, 10000
		dpi := computeSafeDPI(w, h, requested, maxDim)
		longestPx := int(w / 72.0 * float64(dpi))
		if longestPx > maxDim {
			t.Errorf("longest edge %dpx exceeds maxDim %d at dpi %d", longestPx, maxDim, dpi)
		}
	})
}

func TestResolveLanguage(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		available []string
		want      string
	}{
		{"present uses itself", "fra", []string{"eng", "fra", "spa"}, "fra"},
		{"missing falls back to eng", "fra", []string{"eng", "spa"}, "eng"},
		{"missing and no eng uses first", "fra", []string{"spa", "deu"}, "spa"},
		{"empty available keeps requested", "fra", nil, "fra"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveLanguage(tt.requested, tt.available); got != tt.want {
				t.Errorf("resolveLanguage(%q,%v) = %q, want %q", tt.requested, tt.available, got, tt.want)
			}
		})
	}
}

func TestOCRMaxImageDim(t *testing.T) {
	t.Setenv("OCR_MAX_IMAGE_DIM", "5000")
	if got := ocrMaxImageDim(); got != 5000 {
		t.Errorf("OCR_MAX_IMAGE_DIM=5000 → %d, want 5000", got)
	}
	t.Setenv("OCR_MAX_IMAGE_DIM", "0")
	if got := ocrMaxImageDim(); got != 10000 {
		t.Errorf("OCR_MAX_IMAGE_DIM=0 → %d, want default 10000", got)
	}
	t.Setenv("OCR_MAX_IMAGE_DIM", "notnum")
	if got := ocrMaxImageDim(); got != 10000 {
		t.Errorf("invalid OCR_MAX_IMAGE_DIM → %d, want default 10000", got)
	}
	t.Setenv("OCR_MAX_IMAGE_DIM", "")
	if got := ocrMaxImageDim(); got != 10000 {
		t.Errorf("unset OCR_MAX_IMAGE_DIM → %d, want default 10000", got)
	}
}

func TestProcessFileUnsupportedTool(t *testing.T) {
	_, err := ProcessFile(context.Background(), uuid.New(), "unknown-tool", []string{"/tmp/test.pdf"}, nil, t.TempDir())
	if err == nil {
		t.Error("expected error for unsupported tool")
	}
}

func TestOptionString(t *testing.T) {
	opts := map[string]interface{}{"quality": "screen"}
	val, ok := optionString(opts, "quality")
	if !ok || val != "screen" {
		t.Errorf("expected 'screen', got %q ok=%v", val, ok)
	}
}

func TestFindGhostscript(t *testing.T) {
	// Just test it doesn't panic - may or may not find gs on the system
	_, _ = findGhostscript()
}

func TestBuildCompressArgs(t *testing.T) {
	containsArg := func(args []string, arg string) bool {
		for _, a := range args {
			if a == arg {
				return true
			}
		}
		return false
	}

	t.Run("each quality level produces unique args", func(t *testing.T) {
		levels := []string{"low", "medium", "high", "extreme"}
		argSets := make(map[string][]string)
		for _, level := range levels {
			argSets[level] = buildCompressArgs(level, "/out.pdf", "/in.pdf")
		}
		// No two levels should produce identical arg slices
		for i := 0; i < len(levels); i++ {
			for j := i + 1; j < len(levels); j++ {
				a := argSets[levels[i]]
				b := argSets[levels[j]]
				if len(a) == len(b) {
					same := true
					for k := range a {
						if a[k] != b[k] {
							same = false
							break
						}
					}
					if same {
						t.Errorf("%s and %s produced identical args", levels[i], levels[j])
					}
				}
			}
		}
	})

	t.Run("high has lower threshold than medium", func(t *testing.T) {
		medium := buildCompressArgs("medium", "/out.pdf", "/in.pdf")
		high := buildCompressArgs("high", "/out.pdf", "/in.pdf")
		if !containsArg(medium, "-dColorImageDownsampleThreshold=1.5") {
			t.Error("medium should use threshold 1.5")
		}
		if !containsArg(high, "-dColorImageDownsampleThreshold=1.0") {
			t.Error("high should use threshold 1.0")
		}
	})

	t.Run("extreme includes grayscale conversion", func(t *testing.T) {
		extreme := buildCompressArgs("extreme", "/out.pdf", "/in.pdf")
		if !containsArg(extreme, "-dColorConversionStrategy=/Gray") {
			t.Error("extreme should include grayscale conversion")
		}
		if !containsArg(extreme, "-dProcessColorModel=/DeviceGray") {
			t.Error("extreme should set DeviceGray color model")
		}
	})

	t.Run("high and extreme include QFactor", func(t *testing.T) {
		high := buildCompressArgs("high", "/out.pdf", "/in.pdf")
		extreme := buildCompressArgs("extreme", "/out.pdf", "/in.pdf")
		if !containsArg(high, "-c") {
			t.Error("high should include PostScript quality params")
		}
		if !containsArg(extreme, "-c") {
			t.Error("extreme should include PostScript quality params")
		}
	})

	t.Run("low and medium do not include QFactor", func(t *testing.T) {
		low := buildCompressArgs("low", "/out.pdf", "/in.pdf")
		medium := buildCompressArgs("medium", "/out.pdf", "/in.pdf")
		if containsArg(low, "-c") {
			t.Error("low should not include PostScript quality params")
		}
		if containsArg(medium, "-c") {
			t.Error("medium should not include PostScript quality params")
		}
	})

	t.Run("output and input paths are last", func(t *testing.T) {
		args := buildCompressArgs("medium", "/out.pdf", "/in.pdf")
		n := len(args)
		if args[n-2] != "-sOutputFile=/out.pdf" {
			t.Errorf("expected output path second to last, got %s", args[n-2])
		}
		if args[n-1] != "/in.pdf" {
			t.Errorf("expected input path last, got %s", args[n-1])
		}
	})
}

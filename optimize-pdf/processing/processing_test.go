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

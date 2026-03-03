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

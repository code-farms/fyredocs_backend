package processing

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestProcessFileNoInputs(t *testing.T) {
	_, err := ProcessFile(context.Background(), uuid.New(), "pdf-to-text", nil, nil, "", nil)
	if err == nil {
		t.Error("expected error for no input files")
	}
	if err.Error() != "no input files provided" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestProcessFileUnsupportedTool(t *testing.T) {
	_, err := ProcessFile(context.Background(), uuid.New(), "unknown-tool", []string{"/tmp/test.pdf"}, nil, t.TempDir(), nil)
	if err == nil {
		t.Error("expected error for unsupported tool")
	}
}

func TestProcessFileRecognizesODFTools(t *testing.T) {
	tools := []string{"pdf-to-odt", "pdf-to-ods", "pdf-to-odp"}
	for _, tool := range tools {
		t.Run(tool, func(t *testing.T) {
			_, err := ProcessFile(context.Background(), uuid.New(), tool, []string{"/nonexistent.pdf"}, nil, t.TempDir(), nil)
			// Should fail on conversion (no LibreOffice), NOT on "unsupported tool type"
			if err != nil && err.Error() == "unsupported tool type: "+tool {
				t.Errorf("tool %q should be recognized but got unsupported error", tool)
			}
		})
	}
}

func TestProcessFileOutputDirCreation(t *testing.T) {
	dir := t.TempDir() + "/subdir"
	// This will fail because the input file doesn't exist, but the dir should be created
	_, _ = ProcessFile(context.Background(), uuid.New(), "pdf-to-text", []string{"/nonexistent.pdf"}, nil, dir, nil)
	// Just verifying no panic on dir creation
}

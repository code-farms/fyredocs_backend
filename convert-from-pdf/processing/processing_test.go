package processing

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestProcessFileNoInputs(t *testing.T) {
	_, err := ProcessFile(context.Background(), uuid.New(), "pdf-to-text", nil, nil, "")
	if err == nil {
		t.Error("expected error for no input files")
	}
	if err.Error() != "no input files provided" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestProcessFileUnsupportedTool(t *testing.T) {
	_, err := ProcessFile(context.Background(), uuid.New(), "unknown-tool", []string{"/tmp/test.pdf"}, nil, t.TempDir())
	if err == nil {
		t.Error("expected error for unsupported tool")
	}
}

func TestProcessFileOutputDirCreation(t *testing.T) {
	dir := t.TempDir() + "/subdir"
	// This will fail because the input file doesn't exist, but the dir should be created
	_, _ = ProcessFile(context.Background(), uuid.New(), "pdf-to-text", []string{"/nonexistent.pdf"}, nil, dir)
	// Just verifying no panic on dir creation
}

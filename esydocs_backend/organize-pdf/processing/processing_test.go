package processing

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestProcessFileNoInputs(t *testing.T) {
	_, err := ProcessFile(context.Background(), uuid.New(), "merge-pdf", nil, nil, "")
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

func TestParsePageRange(t *testing.T) {
	pages := parsePageRange("1-3,5", 10)
	expected := []int{1, 2, 3, 5}
	if len(pages) != len(expected) {
		t.Fatalf("expected %d pages, got %d", len(expected), len(pages))
	}
	for i, p := range pages {
		if p != expected[i] {
			t.Errorf("page[%d] = %d, want %d", i, p, expected[i])
		}
	}
}

func TestParsePageRangeAll(t *testing.T) {
	pages := parsePageRange("all", 5)
	if len(pages) != 5 {
		t.Errorf("expected 5 pages for 'all', got %d", len(pages))
	}
}

func TestParsePageRangeEmpty(t *testing.T) {
	pages := parsePageRange("", 5)
	if len(pages) != 5 {
		t.Errorf("expected 5 pages for empty range, got %d", len(pages))
	}
}

func TestParsePageRangeInvalid(t *testing.T) {
	pages := parsePageRange("abc", 5)
	if len(pages) != 0 {
		t.Errorf("expected 0 pages for invalid range, got %d", len(pages))
	}
}

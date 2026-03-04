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

func TestParsePageRangeSinglePage(t *testing.T) {
	pages := parsePageRange("3", 10)
	if len(pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(pages))
	}
	if pages[0] != 3 {
		t.Errorf("expected page 3, got %d", pages[0])
	}
}

func TestParsePageRangeOutOfBounds(t *testing.T) {
	pages := parsePageRange("1-20", 5)
	// Pages beyond totalPages should be clamped or excluded
	for _, p := range pages {
		if p > 5 {
			t.Errorf("page %d exceeds total page count 5", p)
		}
	}
}

func TestParsePageRangeMultipleRanges(t *testing.T) {
	pages := parsePageRange("1-2,4-5", 10)
	if len(pages) != 4 {
		t.Errorf("expected 4 pages, got %d: %v", len(pages), pages)
	}
}

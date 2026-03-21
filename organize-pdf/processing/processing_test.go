package processing

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
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

func TestParsePageRangeGroupsAll(t *testing.T) {
	groups := parsePageRangeGroups("all", 5)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if len(groups[0]) != 5 {
		t.Errorf("expected 5 pages in group, got %d", len(groups[0]))
	}
}

func TestParsePageRangeGroupsEmpty(t *testing.T) {
	groups := parsePageRangeGroups("", 5)
	if len(groups) != 1 || len(groups[0]) != 5 {
		t.Errorf("expected 1 group with 5 pages for empty range, got %v", groups)
	}
}

func TestParsePageRangeGroupsMultiple(t *testing.T) {
	groups := parsePageRangeGroups("1-3, 5, 7-10", 10)
	if len(groups) != 3 {
		t.Fatalf("expected 3 groups, got %d: %v", len(groups), groups)
	}
	// Group 1: pages 1,2,3
	if len(groups[0]) != 3 || groups[0][0] != 1 || groups[0][2] != 3 {
		t.Errorf("group 0 = %v, want [1,2,3]", groups[0])
	}
	// Group 2: page 5
	if len(groups[1]) != 1 || groups[1][0] != 5 {
		t.Errorf("group 1 = %v, want [5]", groups[1])
	}
	// Group 3: pages 7,8,9,10
	if len(groups[2]) != 4 || groups[2][0] != 7 || groups[2][3] != 10 {
		t.Errorf("group 2 = %v, want [7,8,9,10]", groups[2])
	}
}

func TestParsePageRangeGroupsSinglePages(t *testing.T) {
	groups := parsePageRangeGroups("2,4,6", 10)
	if len(groups) != 3 {
		t.Fatalf("expected 3 groups, got %d", len(groups))
	}
	for i, g := range groups {
		if len(g) != 1 {
			t.Errorf("group %d should have 1 page, got %d", i, len(g))
		}
	}
}

func TestParsePageRangeGroupsInvalid(t *testing.T) {
	groups := parsePageRangeGroups("abc", 5)
	if len(groups) != 0 {
		t.Errorf("expected 0 groups for invalid input, got %d", len(groups))
	}
}

func TestParsePageRangeGroupsOutOfBounds(t *testing.T) {
	groups := parsePageRangeGroups("1-3, 8-20", 5)
	// "1-3" valid, "8-20" invalid (exceeds maxPages=5)
	if len(groups) != 1 {
		t.Fatalf("expected 1 valid group, got %d: %v", len(groups), groups)
	}
	if len(groups[0]) != 3 {
		t.Errorf("expected 3 pages in group, got %d", len(groups[0]))
	}
}

func TestSplitEqualGroups(t *testing.T) {
	groups := splitEqualGroups(10, 4)
	if len(groups) != 4 {
		t.Fatalf("expected 4 groups, got %d", len(groups))
	}
	// 10/4 = 2 base, 2 extra → sizes 3,3,2,2
	expectedSizes := []int{3, 3, 2, 2}
	for i, g := range groups {
		if len(g) != expectedSizes[i] {
			t.Errorf("group %d: expected %d pages, got %d: %v", i, expectedSizes[i], len(g), g)
		}
	}
	// Verify all pages 1-10 are covered
	all := []int{}
	for _, g := range groups {
		all = append(all, g...)
	}
	if len(all) != 10 {
		t.Errorf("expected 10 total pages, got %d", len(all))
	}
	for i, p := range all {
		if p != i+1 {
			t.Errorf("page at index %d = %d, want %d", i, p, i+1)
		}
	}
}

func TestSplitEqualGroupsExact(t *testing.T) {
	groups := splitEqualGroups(9, 3)
	if len(groups) != 3 {
		t.Fatalf("expected 3 groups, got %d", len(groups))
	}
	for i, g := range groups {
		if len(g) != 3 {
			t.Errorf("group %d: expected 3 pages, got %d", i, len(g))
		}
	}
}

func TestSplitEqualGroupsMorePartsThanPages(t *testing.T) {
	groups := splitEqualGroups(3, 5)
	// Capped to 3 parts (one page each)
	if len(groups) != 3 {
		t.Fatalf("expected 3 groups (capped), got %d", len(groups))
	}
	for i, g := range groups {
		if len(g) != 1 {
			t.Errorf("group %d: expected 1 page, got %d", i, len(g))
		}
	}
}

func TestZipDirectoryEmptyDir(t *testing.T) {
	sourceDir := t.TempDir()
	zipPath := filepath.Join(t.TempDir(), "out.zip")
	err := zipDirectory(sourceDir, zipPath)
	if err == nil {
		t.Fatal("expected error for empty source directory")
	}
}

func TestZipDirectoryWithFiles(t *testing.T) {
	sourceDir := t.TempDir()
	// Create two test files in the source directory.
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(sourceDir, name), []byte("hello"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	zipPath := filepath.Join(t.TempDir(), "out.zip")
	if err := zipDirectory(sourceDir, zipPath); err != nil {
		t.Fatalf("zipDirectory failed: %v", err)
	}

	// Verify the zip contains 2 entries.
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("failed to open zip: %v", err)
	}
	defer r.Close()
	if len(r.File) != 2 {
		t.Errorf("expected 2 entries in zip, got %d", len(r.File))
	}
}

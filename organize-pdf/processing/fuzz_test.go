// Fuzz targets for the page-range parser and the signature data-URL decoder —
// the two places arbitrary client strings steer file operations in this
// worker. Invariants: no panic, page numbers stay inside the document, and a
// decoded image can only ever land inside the directory it was given.
package processing

import (
	"path/filepath"
	"strings"
	"testing"
)

func FuzzParsePageRange(f *testing.F) {
	f.Add("1-3,5", 10)
	f.Add("all", 5)
	f.Add("", 3)
	f.Add("0", 5)
	f.Add("5-1", 9)
	f.Add("1-999999999", 4)
	f.Add("1-2-3", 9)
	f.Add(",,,", 3)
	f.Add("-1--2", 8)
	f.Add(" 2 , 4-4 ", 6)
	f.Fuzz(func(t *testing.T, rangeStr string, maxPages int) {
		// maxPages comes from a real PDF's page count in production; the
		// clamp is a driver artifact so the fuzzer cannot allocate MaxInt.
		if maxPages < 0 {
			maxPages = -maxPages
		}
		maxPages %= 512

		pages := parsePageRange(rangeStr, maxPages)
		seen := make(map[int]bool, len(pages))
		for _, p := range pages {
			if p < 1 || p > maxPages {
				t.Errorf("parsePageRange(%q, %d) produced out-of-range page %d", rangeStr, maxPages, p)
			}
			if seen[p] {
				t.Errorf("parsePageRange(%q, %d) produced duplicate page %d", rangeStr, maxPages, p)
			}
			seen[p] = true
		}
	})
}

func FuzzParsePageRangeGroups(f *testing.F) {
	f.Add("1-3,5,7-10", 12)
	f.Add("all", 4)
	f.Add("", 2)
	f.Add("3-1,0,x", 9)
	f.Fuzz(func(t *testing.T, rangeStr string, maxPages int) {
		if maxPages < 0 {
			maxPages = -maxPages
		}
		// [1,512]: a real document always has at least one page (maxPages is
		// pdfcpu's page count), and 0 would make the "all" branch emit one
		// empty group — a driver artifact, not a reachable state.
		maxPages = maxPages%512 + 1

		for _, group := range parsePageRangeGroups(rangeStr, maxPages) {
			if len(group) == 0 {
				t.Errorf("parsePageRangeGroups(%q, %d) produced an empty group — one empty output PDF per empty group", rangeStr, maxPages)
			}
			for _, p := range group {
				if p < 1 || p > maxPages {
					t.Errorf("parsePageRangeGroups(%q, %d) produced out-of-range page %d", rangeStr, maxPages, p)
				}
			}
		}
	})
}

func FuzzDecodeDataURLImage(f *testing.F) {
	f.Add("data:image/png;base64,iVBORw0KGgo=")
	f.Add("data:image/webp;base64,AAAA")
	f.Add("data:image/png;base64,!!!not-base64!!!")
	f.Add("no data url at all")
	f.Add("data:;base64,")
	f.Add("data:image/svg+xml;base64,PHN2Zz4=")
	f.Fuzz(func(t *testing.T, dataURL string) {
		dir := t.TempDir()
		path, err := decodeDataURLImage(dataURL, dir, "sig", true)
		if err != nil {
			return
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Errorf("decoded image escaped its directory: %q (dir %q)", path, dir)
		}
	})
}

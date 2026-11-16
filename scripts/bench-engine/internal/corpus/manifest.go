// Package corpus is the benchmark corpus: the committed manifest of recipes,
// the generators that turn recipes into files, and the lock that pins what was
// generated (docs/developer/architecture/next-generation-document-engine/
// 16-benchmarking-and-testing.md §5.2).
//
// The manifest holds RECIPES, not checksums, because generated documents are
// not byte-stable: LibreOffice and pdfcpu stamp IDs and dates into every file.
// Checksums of what a particular `gen` produced live in the local lock file, and
// `verify` compares the files against that lock before any run, so a run never
// measures a file that changed underneath it. Real-world (donated) files will be
// listed with a fixed sha256 instead; until they exist their series are marked
// "pending" and skipped.
package corpus

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// StatusPending marks a series that has no files yet (real-world documents to
// be added later). It is listed so the gap is visible and skipped everywhere.
const StatusPending = "pending"

// Series is one manifest entry: Count files of one Category, made by one
// Generator with Params, each run through Tools.
type Series struct {
	ID        string            `json:"id"`
	Category  string            `json:"category"`
	Generator string            `json:"generator"`
	Count     int               `json:"count"`
	Params    map[string]string `json:"params,omitempty"`
	Tools     []string          `json:"tools"`
	Status    string            `json:"status,omitempty"`
	// Expect is "ok" (default: every job must complete) or "fail-or-ok" for
	// inputs that may legitimately be rejected, such as malformed PDFs; for
	// those a clean failure with an error code passes, a timeout does not.
	Expect string `json:"expect,omitempty"`
	Note   string `json:"note,omitempty"`
}

// Manifest is the committed corpus definition.
type Manifest struct {
	Version int      `json:"version"`
	Series  []Series `json:"series"`
}

// Item is one corpus file: series ID plus its 1-based index.
type Item struct {
	ID     string // e.g. "pdf-small-003"
	Index  int
	Series Series
}

// Load reads and validates a manifest. Validation is strict because a typo in a
// generator or tool name would otherwise silently drop files from every run.
func Load(path string, generators map[string]Generator) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("manifest %s: %w", path, err)
	}
	return &m, m.Validate(generators)
}

// Validate checks IDs are unique, counts positive, tools present and every
// generator of a non-pending series known.
func (m *Manifest) Validate(generators map[string]Generator) error {
	seen := map[string]bool{}
	for _, s := range m.Series {
		if s.ID == "" {
			return fmt.Errorf("series with empty id")
		}
		if seen[s.ID] {
			return fmt.Errorf("duplicate series id %q", s.ID)
		}
		seen[s.ID] = true
		if s.Status == StatusPending {
			continue
		}
		if s.Count < 1 {
			return fmt.Errorf("series %q: count must be >= 1", s.ID)
		}
		if len(s.Tools) == 0 {
			return fmt.Errorf("series %q: no tools", s.ID)
		}
		if _, ok := generators[s.Generator]; !ok {
			return fmt.Errorf("series %q: unknown generator %q", s.ID, s.Generator)
		}
	}
	return nil
}

// Items expands every non-pending series into its files, in manifest order.
func (m *Manifest) Items() []Item {
	var out []Item
	for _, s := range m.Series {
		if s.Status == StatusPending {
			continue
		}
		for i := 1; i <= s.Count; i++ {
			out = append(out, Item{ID: fmt.Sprintf("%s-%03d", s.ID, i), Index: i, Series: s})
		}
	}
	return out
}

// Pending lists the IDs of pending series, sorted.
func (m *Manifest) Pending() []string {
	var p []string
	for _, s := range m.Series {
		if s.Status == StatusPending {
			p = append(p, s.ID)
		}
	}
	sort.Strings(p)
	return p
}

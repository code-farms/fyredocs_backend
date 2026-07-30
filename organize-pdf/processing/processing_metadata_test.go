package processing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// TestParseMetadataOptions verifies the worker-side defensive parse: option
// keys map onto the exact Info-dict names, values are trimmed and capped, and
// an effectively-empty payload errors instead of producing a no-op job.
func TestParseMetadataOptions(t *testing.T) {
	props, err := parseMetadataOptions(map[string]interface{}{
		"title":    "  Annual Report  ",
		"author":   "Prachi",
		"subject":  "",
		"keywords": "tax, 2026",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if props["Title"] != "Annual Report" {
		t.Errorf("Title = %q, want trimmed value", props["Title"])
	}
	if props["Author"] != "Prachi" || props["Keywords"] != "tax, 2026" {
		t.Errorf("props = %v", props)
	}
	if _, ok := props["Subject"]; ok {
		t.Error("empty subject must be dropped, not written")
	}

	long := strings.Repeat("y", maxMetadataFieldLen+50)
	props, err = parseMetadataOptions(map[string]interface{}{"title": long})
	if err != nil {
		t.Fatalf("parse long: %v", err)
	}
	if len(props["Title"]) != maxMetadataFieldLen {
		t.Errorf("Title length = %d, want capped at %d", len(props["Title"]), maxMetadataFieldLen)
	}

	if _, err := parseMetadataOptions(map[string]interface{}{"title": "   "}); err == nil {
		t.Error("all-empty options must error")
	}
	if _, err := parseMetadataOptions(nil); err == nil {
		t.Error("nil options must error")
	}
}

// TestSetPDFMetadata is the end-to-end proof for the pdfcpu route this tool
// relies on: PropertiesAdd writes standard Info-dict keys and the writer
// preserves them — read the output back and assert the round trip. Pure Go,
// no external binaries, runs everywhere.
func TestSetPDFMetadata(t *testing.T) {
	dir := t.TempDir()
	png := filepath.Join(dir, "page.png")
	writeTestPNG(t, png, 200, 300)

	input := filepath.Join(dir, "in.pdf")
	if err := api.ImportImagesFile([]string{png}, input, nil, model.NewDefaultConfiguration()); err != nil {
		t.Fatalf("build input pdf: %v", err)
	}

	output := filepath.Join(dir, "out.pdf")
	err := setPDFMetadata(input, output, map[string]interface{}{
		"title":    "Scanned Contract",
		"author":   "FyreDocs",
		"keywords": "contract, signed",
	})
	if err != nil {
		t.Fatalf("setPDFMetadata: %v", err)
	}

	f, err := os.Open(output)
	if err != nil {
		t.Fatalf("open output: %v", err)
	}
	defer f.Close()
	// Standard Info keys parse into PDFInfo fields on read (NOT into the
	// custom-properties map) — read back through the same path a viewer uses.
	info, err := api.PDFInfo(f, output, nil, model.NewDefaultConfiguration())
	if err != nil {
		t.Fatalf("read info: %v", err)
	}
	if info.Title != "Scanned Contract" || info.Author != "FyreDocs" {
		t.Errorf("round trip mismatch: title=%q author=%q", info.Title, info.Author)
	}
	if len(info.Keywords) == 0 || !strings.Contains(strings.Join(info.Keywords, ","), "contract") {
		t.Errorf("keywords round trip mismatch: %v", info.Keywords)
	}
}

// TestSetPDFMetadataEmptyOptions pins the failure mode: nothing to write is
// an error, not a silent byte-for-byte copy that looks like success.
func TestSetPDFMetadataEmptyOptions(t *testing.T) {
	if err := setPDFMetadata("in.pdf", "out.pdf", map[string]interface{}{}); err == nil {
		t.Error("empty options must error before touching any file")
	}
}

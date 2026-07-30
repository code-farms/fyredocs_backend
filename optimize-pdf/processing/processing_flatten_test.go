package processing

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestBuildFlattenArgs pins the Ghostscript invocation: -dPreserveAnnots=false
// is the switch that actually flattens (drop it and the tool becomes a no-op
// copy), and -dPrinted selects print appearances so no-print annotations are
// excluded rather than baked in.
func TestBuildFlattenArgs(t *testing.T) {
	args := buildFlattenArgs("/out.pdf", "/in.pdf")

	want := map[string]bool{
		"-sDEVICE=pdfwrite":            false,
		"-dPreserveAnnots=false":       false,
		"-dPrinted":                    false,
		"-dSAFER":                      false,
		"-sOutputFile=/out.pdf":        false,
		"-dDetectDuplicateImages=true": false,
	}
	for _, a := range args {
		if _, ok := want[a]; ok {
			want[a] = true
		}
	}
	for arg, seen := range want {
		if !seen {
			t.Errorf("missing argument %q", arg)
		}
	}
	if args[len(args)-1] != "/in.pdf" {
		t.Errorf("input path must be the final argument, got %q", args[len(args)-1])
	}
}

// TestFlattenPDFIntegration exercises the real Ghostscript binary when one is
// installed (the Alpine image always has it; developer machines may not —
// hence the skip, matching the established gs-test posture in this module).
func TestFlattenPDFIntegration(t *testing.T) {
	if _, err := findGhostscript(); err != nil {
		t.Skip("ghostscript not installed")
	}

	dir := t.TempDir()
	input := filepath.Join(dir, "in.pdf")
	// Minimal single-page PDF — enough for gs to re-distill.
	minimalPDF := "%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]>>endobj\nxref\n0 4\n0000000000 65535 f \n0000000009 00000 n \n0000000052 00000 n \n0000000101 00000 n \ntrailer<</Size 4/Root 1 0 R>>\nstartxref\n164\n%%EOF\n"
	if err := os.WriteFile(input, []byte(minimalPDF), 0o644); err != nil {
		t.Fatal(err)
	}

	output := filepath.Join(dir, "out.pdf")
	if err := flattenPDF(context.Background(), input, output); err != nil {
		t.Fatalf("flattenPDF: %v", err)
	}
	info, err := os.Stat(output)
	if err != nil || info.Size() == 0 {
		t.Fatalf("output missing or empty: %v", err)
	}
}

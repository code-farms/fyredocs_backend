package processing

import (
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

// NOTE: the real-Ghostscript flatten test lives in
// processing_binaries_test.go (TestFlattenPDFBinaries) under the
// `integration && binaries` tags — the pre-taxonomy skip-when-missing gate
// that lived here migrated there per
// docs/developer/architecture/testing-strategy.md.

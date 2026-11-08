package processing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// TestPdfcpuOpsHonourCancelledContext proves the job context reaches pdfcpu
// (v0.16+): a cancelled job stops instead of running to completion past its
// deadline, and writes no output.
func TestPdfcpuOpsHonourCancelledContext(t *testing.T) {
	dir := t.TempDir()
	png := filepath.Join(dir, "a.png")
	writeTestPNG(t, png, 200, 300)
	input := filepath.Join(dir, "in.pdf")
	if err := api.ImportImagesFile(t.Context(), []string{png, png}, input, nil, nil); err != nil {
		t.Fatalf("build input: %v", err)
	}

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	ops := map[string]func(out string) error{
		"merge":  func(out string) error { return mergePDFs(cancelled, []string{input, input}, out) },
		"rotate": func(out string) error { return rotatePDF(cancelled, input, out, 90, "all") },
		"remove": func(out string) error { return removePages(cancelled, input, out, "1") },
	}
	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(dir, name+".pdf")
			err := op(out)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
			if _, statErr := os.Stat(out); statErr == nil {
				t.Errorf("cancelled %s wrote %s", name, out)
			}
		})
	}

	// Control: the same operation with a live context succeeds.
	if err := rotatePDF(t.Context(), input, filepath.Join(dir, "ok.pdf"), 90, "all"); err != nil {
		t.Fatalf("rotate with live ctx: %v", err)
	}
}

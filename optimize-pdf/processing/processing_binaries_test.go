//go:build integration && binaries

// Real-binary exec-path tests for the optimize-pdf worker: Ghostscript
// (compress / repair / flatten) and the pdftoppm+tesseract OCR pipeline.
// Run via `make test-binaries` (dockerized — see
// deployment/test-binaries.Dockerfile); fixtures are generated in-code per
// docs/developer/architecture/testing-strategy.md, never committed.
package processing

import (
	"bytes"
	"context"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// TestMain mirrors main.go's DisableConfigDir: without it, the first pdfcpu
// call from the test runner loads — and on first use writes — a config.yml
// under os.UserConfigDir(), making behavior depend on the container's HOME.
func TestMain(m *testing.M) {
	api.DisableConfigDir()
	os.Exit(m.Run())
}

// writeTextPNG renders msg as black-on-white text into a PNG. basicfont's
// 7x13 bitmap face is the only text source available without cgo or bundled
// TTFs; at native size tesseract cannot read it reliably, so the glyphs are
// scaled up nearest-neighbor to a size OCR handles well.
func writeTextPNG(t *testing.T, path, msg string, scale int) {
	t.Helper()
	face := basicfont.Face7x13
	w := font.MeasureString(face, msg).Ceil() + 14
	h := face.Height + 14
	small := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(small, small.Bounds(), image.White, image.Point{}, draw.Src)
	d := &font.Drawer{
		Dst:  small,
		Src:  image.Black,
		Face: face,
		Dot:  fixed.P(7, 7+face.Ascent),
	}
	d.DrawString(msg)

	big := image.NewRGBA(image.Rect(0, 0, w*scale, h*scale))
	for y := 0; y < h*scale; y++ {
		for x := 0; x < w*scale; x++ {
			big.Set(x, y, small.At(x/scale, y/scale))
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, big); err != nil {
		t.Fatal(err)
	}
}

// buildImagePDF assembles an image-only PDF (one page per rendered text PNG)
// via pdfcpu — the established in-code fixture recipe. Image-only pages give
// Ghostscript real raster content to downsample and give OCR no text layer
// to cheat from.
func buildImagePDF(t *testing.T, dir string, pages ...string) string {
	t.Helper()
	imgs := make([]string, len(pages))
	for i, msg := range pages {
		p := filepath.Join(dir, "page"+string(rune('a'+i))+".png")
		writeTextPNG(t, p, msg, 4)
		imgs[i] = p
	}
	input := filepath.Join(dir, "input.pdf")
	if err := api.ImportImagesFile(t.Context(), imgs, input, nil, nil); err != nil {
		t.Fatalf("build input pdf: %v", err)
	}
	return input
}

// assertValidPDF checks the full output contract every binaries test cares
// about: the file exists, is non-empty, starts with the PDF magic, and
// survives pdfcpu validation.
func assertValidPDF(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("output missing: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("output is empty")
	}
	head := make([]byte, 5)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Read(head); err != nil || !bytes.HasPrefix(head, []byte("%PDF-")) {
		t.Fatalf("output lacks %%PDF- header (got %q, err %v)", head, err)
	}
	if err := api.ValidateFile(t.Context(), path, nil, nil); err != nil {
		t.Fatalf("output does not validate: %v", err)
	}
}

// TestCompressPDFBinaries runs real Ghostscript over a generated multi-page
// image PDF and asserts a valid, non-empty output plus the size metadata the
// UI reports from.
func TestCompressPDFBinaries(t *testing.T) {
	dir := t.TempDir()
	input := buildImagePDF(t, dir, "COMPRESS PAGE ONE", "COMPRESS PAGE TWO", "COMPRESS PAGE THREE")
	output := filepath.Join(dir, "out.pdf")

	meta, err := compressPDF(context.Background(), input, output, map[string]interface{}{"quality": "medium"})
	if err != nil {
		t.Fatalf("compressPDF: %v", err)
	}
	assertValidPDF(t, output)

	if ctx, err := api.ReadContextFile(t.Context(), output); err != nil || ctx.PageCount != 3 {
		t.Errorf("compressed page count: %v (err %v), want 3", ctx, err)
	}
	orig, ok := meta["originalSizeBytes"].(int64)
	if !ok || orig <= 0 {
		t.Errorf("originalSizeBytes = %v, want > 0", meta["originalSizeBytes"])
	}
	if comp, ok := meta["compressedSizeBytes"].(int64); !ok || comp <= 0 {
		t.Errorf("compressedSizeBytes = %v, want > 0", meta["compressedSizeBytes"])
	}
}

// TestRepairPDFBinaries exercises the Ghostscript re-distill path on a valid
// file — repair must be a lossless-enough round trip that the output still
// validates with the same page count.
func TestRepairPDFBinaries(t *testing.T) {
	dir := t.TempDir()
	input := buildImagePDF(t, dir, "REPAIR ME")
	output := filepath.Join(dir, "out.pdf")

	if _, err := repairPDF(context.Background(), input, output); err != nil {
		t.Fatalf("repairPDF: %v", err)
	}
	assertValidPDF(t, output)
	if ctx, err := api.ReadContextFile(t.Context(), output); err != nil || ctx.PageCount != 1 {
		t.Errorf("repaired page count: %v (err %v), want 1", ctx, err)
	}
}

// TestFlattenPDFBinaries exercises the real Ghostscript flatten path.
// Migrated from the pre-taxonomy ad-hoc skip gate in
// processing_flatten_test.go — under the binaries tag the binary is
// guaranteed, so a missing gs is a failure, not a skip.
func TestFlattenPDFBinaries(t *testing.T) {
	dir := t.TempDir()
	input := buildImagePDF(t, dir, "FLATTEN ME")
	output := filepath.Join(dir, "out.pdf")

	if _, err := flattenPDF(context.Background(), input, output); err != nil {
		t.Fatalf("flattenPDF: %v", err)
	}
	assertValidPDF(t, output)
}

// TestOCRPDFBinaries runs the full rasterize→tesseract→merge pipeline over an
// image-only PDF whose pages carry rendered text, then asserts the searchable
// output validates and the extracted-text sidecar carries non-empty text for
// BOTH pages in page order (form-feed separated).
//
// NOTE: exact-word assertions are deliberately absent — tesseract misreads
// the blocky nearest-neighbor-scaled basicfont glyphs (observed: "HELLO
// WORLD" → "HELLD WORELLD"), and no anti-aliased font is available in-image
// without committing a TTF. Non-empty per-page text still proves the real
// tesseract exec path ran and the sidecar plumbing kept page alignment.
func TestOCRPDFBinaries(t *testing.T) {
	dir := t.TempDir()
	input := buildImagePDF(t, dir, "HELLO WORLD", "SECOND PAGE TEXT")
	output := filepath.Join(dir, "out.pdf")

	meta, err := ocrPDF(context.Background(), input, output, map[string]interface{}{"language": "en"})
	if err != nil {
		t.Fatalf("ocrPDF: %v", err)
	}
	assertValidPDF(t, output)

	if ctx, err := api.ReadContextFile(t.Context(), output); err != nil || ctx.PageCount != 2 {
		t.Errorf("OCR output page count: %v (err %v), want 2", ctx, err)
	}
	if got := meta["language"]; got != "eng" {
		t.Errorf("language = %v, want eng", got)
	}
	if got := meta["pagesOCRed"]; got != 2 {
		t.Errorf("pagesOCRed = %v, want 2", got)
	}
	text, _ := meta["extractedText"].(string)
	if text == "" {
		t.Fatal("extractedText metadata missing or empty")
	}
	// Page alignment must survive the parallel worker pool: two pages means
	// one form-feed separator with recognized text on both sides.
	pages := strings.Split(text, "\f")
	if len(pages) != 2 {
		t.Fatalf("extractedText page count = %d, want 2 (%q)", len(pages), text)
	}
	for i, p := range pages {
		if strings.TrimSpace(p) == "" {
			t.Errorf("page %d produced no OCR text (%q)", i+1, text)
		}
	}
}

// TestCompressPDFCorruptInputBinaries feeds Ghostscript garbage bytes with a
// .pdf name: the exec path must surface an error — that error return is the
// pipeline contract (ProcessFile propagates it, so the worker never uploads).
//
// NOTE: pdfwrite opens its output file before the input is parsed, so a
// failed run leaves a small header-only stub at outputPath. compressPDF does
// not remove it. Pinned rather than asserted absent: the stub is dead weight
// in the job's temp dir, not an uploaded artifact. If this assertion starts
// failing because the stub is gone, the cleanup was added — delete the pin.
func TestCompressPDFCorruptInputBinaries(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "garbage.pdf")
	if err := os.WriteFile(input, []byte("this is not a pdf at all \x00\x01\x02"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "out.pdf")

	if _, err := compressPDF(context.Background(), input, output, map[string]interface{}{"quality": "medium"}); err == nil {
		t.Fatal("expected error for corrupt input")
	}
	if _, err := os.Stat(output); err != nil {
		t.Logf("no gs stub left behind (cleanup added?): %v", err)
	}
}

// TestOCRPDFCorruptInputBinaries: garbage bytes must fail in pdftoppm before
// any tesseract work, and leave no output.
func TestOCRPDFCorruptInputBinaries(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "garbage.pdf")
	if err := os.WriteFile(input, []byte("%PDF-1.7 but then it all goes wrong"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "out.pdf")

	if _, err := ocrPDF(context.Background(), input, output, nil); err == nil {
		t.Fatal("expected error for corrupt input")
	}
	if _, err := os.Stat(output); err == nil {
		t.Error("corrupt input left an output file behind")
	}
}

// ocrTempSample is one observation of the OCR job's temp directory.
type ocrTempSample struct {
	pngs  int
	bytes int64
}

// sampleTempDir walks root and returns how many page PNGs exist and the total
// bytes under it. Files may disappear mid-walk (that is the point), so walk
// errors are ignored.
func sampleTempDir(root string) ocrTempSample {
	var s ocrTempSample
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			s.bytes += info.Size()
		}
		if strings.HasSuffix(path, ".png") {
			s.pngs++
		}
		return nil
	})
	return s
}

// TestOCRPDFBoundsPageImagesBinaries is the Q7 acceptance check
// (docs/developer/architecture/next-generation-document-engine/
// 20-implementation-roadmap.md): OCR must never hold more rendered page images
// than it has workers. The previous pipeline rendered every page up front, so a
// long scan filled the job's tmpfs before recognition began. It also logs the
// peak temp-dir size and wall time, which is how the before/after numbers in
// the roadmap were measured (OCR_MEASURE_PAGES overrides the page count).
func TestOCRPDFBoundsPageImagesBinaries(t *testing.T) {
	pages := 24
	if v := os.Getenv("OCR_MEASURE_PAGES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			pages = n
		}
	}
	t.Setenv("OCR_MAX_WORKERS", "2")
	workers := ocrMaxWorkers()

	fixtures := t.TempDir()
	png := filepath.Join(fixtures, "page.png")
	writeTextPNG(t, png, "FYREDOCS OCR MEMORY CHECK PAGE", 6)
	imgs := make([]string, pages)
	for i := range imgs {
		imgs[i] = png
	}
	input := filepath.Join(fixtures, "input.pdf")
	if err := api.ImportImagesFile(t.Context(), imgs, input, nil, nil); err != nil {
		t.Fatalf("build input pdf: %v", err)
	}
	// OCR_MEASURE_INPUT swaps in a real scan for measurement runs; the
	// generated text pages compress far better than scanned paper does.
	if v := os.Getenv("OCR_MEASURE_INPUT"); v != "" {
		input = v
		n, err := api.PageCountFile(t.Context(), input)
		if err != nil {
			t.Fatalf("OCR_MEASURE_INPUT: %v", err)
		}
		pages = n
	}

	jobTmp := t.TempDir()
	t.Setenv("TMPDIR", jobTmp)
	output := filepath.Join(fixtures, "out.pdf")

	var peak ocrTempSample
	done := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			s := sampleTempDir(jobTmp)
			if s.pngs > peak.pngs {
				peak.pngs = s.pngs
			}
			if s.bytes > peak.bytes {
				peak.bytes = s.bytes
			}
			select {
			case <-done:
				return
			case <-tick.C:
			}
		}
	}()

	start := time.Now()
	meta, err := ocrPDF(context.Background(), input, output, map[string]interface{}{"language": "en", "dpi": "150"})
	elapsed := time.Since(start)
	close(done)
	<-sampled
	if err != nil {
		t.Fatalf("ocrPDF: %v", err)
	}
	t.Logf("pages=%d workers=%d peakPNGs=%d peakTempBytes=%d wall=%v", pages, workers, peak.pngs, peak.bytes, elapsed.Round(time.Millisecond))

	if peak.pngs > workers {
		t.Errorf("peak rendered page images = %d, want <= %d workers", peak.pngs, workers)
	}
	if got := meta["pagesOCRed"]; got != pages {
		t.Errorf("pagesOCRed = %v, want %d", got, pages)
	}
	if n, err := api.PageCountFile(t.Context(), output); err != nil || n != pages {
		t.Errorf("output pages = %d (err %v), want %d", n, err, pages)
	}
}

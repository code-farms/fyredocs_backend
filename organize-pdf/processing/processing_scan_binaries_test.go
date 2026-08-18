//go:build integration && binaries

// Real-binary exec-path tests for the organize-pdf scan-to-pdf OCR path
// (tesseract). Run via `make test-binaries` (dockerized — see
// deployment/test-binaries.Dockerfile); fixtures are generated in-code per
// docs/developer/architecture/testing-strategy.md.
package processing

import (
	"bytes"
	"context"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// writeTextImagePNG renders msg as black-on-white text into a PNG (the plain
// writeTestPNG helper draws a featureless field — useless to tesseract).
// basicfont's 7x13 bitmap face is the only text source available without cgo
// or bundled TTFs; at native size tesseract cannot read it reliably, so the
// glyphs are scaled up nearest-neighbor to a size OCR handles well.
func writeTextImagePNG(t *testing.T, path, msg string, scale int) {
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

// assertValidScanPDF checks the output contract: exists, non-empty, %PDF-
// magic, and pdfcpu validation.
func assertValidScanPDF(t *testing.T, path string) {
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
	if err := api.ValidateFile(path, nil); err != nil {
		t.Fatalf("output does not validate: %v", err)
	}
}

// TestScanToPDFOCRBinaries drives scanToPDF's real tesseract path (lang eng)
// over two rendered-text pages and asserts a valid searchable PDF plus a
// page-aligned extractedText metadata sidecar.
//
// NOTE: exact-word assertions are deliberately absent — tesseract misreads
// the blocky nearest-neighbor-scaled basicfont glyphs (observed: "HELLO
// WORLD" → "HELLO WOELD"), and no anti-aliased font is available in-image
// without committing a TTF. Non-empty per-page text still proves the real
// tesseract exec path ran and the sidecar plumbing kept page alignment.
func TestScanToPDFOCRBinaries(t *testing.T) {
	dir := t.TempDir()
	p1 := filepath.Join(dir, "p1.png")
	p2 := filepath.Join(dir, "p2.png")
	writeTextImagePNG(t, p1, "HELLO WORLD", 4)
	writeTextImagePNG(t, p2, "SECOND PAGE TEXT", 4)
	output := filepath.Join(dir, "out.pdf")

	meta, err := scanToPDF(context.Background(), []string{p1, p2}, output, map[string]interface{}{
		"ocr":      true,
		"language": "eng",
	})
	if err != nil {
		t.Fatalf("scanToPDF: %v", err)
	}
	assertValidScanPDF(t, output)

	if ctx, err := api.ReadContextFile(output); err != nil || ctx.PageCount != 2 {
		t.Errorf("OCR output page count: %v (err %v), want 2", ctx, err)
	}
	text, _ := meta["extractedText"].(string)
	if text == "" {
		t.Fatal("extractedText metadata missing or empty")
	}
	// Two pages means one form-feed separator with recognized text on both
	// sides — this is the page-alignment contract collectOCRText guarantees.
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

// TestScanToPDFOCRPreprocessedBinaries combines the pure-Go preprocessing
// pipeline (bw threshold) with the real tesseract pass — the production shape
// for a phone-scanned page — and asserts OCR still reads the thresholded text.
func TestScanToPDFOCRPreprocessedBinaries(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "page.png")
	writeTextImagePNG(t, in, "THRESHOLD TEXT", 4)
	output := filepath.Join(dir, "out.pdf")

	meta, err := scanToPDF(context.Background(), []string{in}, output, map[string]interface{}{
		"ocr":      true,
		"language": "eng",
		"enhance":  "bw",
	})
	if err != nil {
		t.Fatalf("scanToPDF: %v", err)
	}
	assertValidScanPDF(t, output)
	// Exact words are not asserted (basicfont misreads — see the NOTE on
	// TestScanToPDFOCRBinaries); non-empty text proves tesseract read the
	// thresholded page rather than falling back to plain image import.
	if text, _ := meta["extractedText"].(string); strings.TrimSpace(text) == "" {
		t.Error("extractedText missing: OCR produced no text from the thresholded page")
	}
}

// TestScanToPDFOCRCorruptImageBinaries: garbage bytes with a .png name must
// fail the job — tesseract rejects the file and the pdfcpu fallback cannot
// import it either — leaving no output behind.
func TestScanToPDFOCRCorruptImageBinaries(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "garbage.png")
	if err := os.WriteFile(in, []byte("not an image \x00\xff\xfe"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "out.pdf")

	if _, err := scanToPDF(context.Background(), []string{in}, output, map[string]interface{}{"ocr": true}); err == nil {
		t.Fatal("expected error for corrupt image input")
	}
	if _, err := os.Stat(output); err == nil {
		t.Error("corrupt input left an output file behind")
	}
}

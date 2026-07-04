package processing

import (
	"context"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"organize-pdf/internal/imaging"
)

func TestParseScanOptionsDefaults(t *testing.T) {
	opts := parseScanOptions(nil)
	if opts.OCR || opts.Language != "" || opts.PageSize != "" || opts.Enhance != "" || len(opts.Pages) != 0 {
		t.Errorf("empty options should yield zero values, got %+v", opts)
	}
}

func TestParseScanOptionsClamping(t *testing.T) {
	opts := parseScanOptions(map[string]interface{}{
		"ocr":      true,
		"language": "KLINGON",
		"pageSize": "A9",
		"enhance":  "sparkle",
		"pages": []interface{}{
			map[string]interface{}{"rotation": 45},
			map[string]interface{}{
				"rotation": 90,
				"corners": map[string]interface{}{
					"tl": map[string]interface{}{"x": -0.5, "y": 0.0},
					"tr": map[string]interface{}{"x": 1.5, "y": 0.0},
					"br": map[string]interface{}{"x": 1.0, "y": 1.0},
					"bl": map[string]interface{}{"x": 0.0, "y": 1.0},
				},
			},
			map[string]interface{}{
				// Partial corners → dropped.
				"corners": map[string]interface{}{
					"tl": map[string]interface{}{"x": 0.1, "y": 0.1},
				},
			},
		},
	})

	if !opts.OCR {
		t.Error("ocr should survive")
	}
	if opts.Language != "" {
		t.Errorf("bad language should clear, got %q", opts.Language)
	}
	if opts.PageSize != "" {
		t.Errorf("bad pageSize should clear, got %q", opts.PageSize)
	}
	if opts.Enhance != "" {
		t.Errorf("bad enhance should clear, got %q", opts.Enhance)
	}
	if opts.Pages[0].Rotation != 0 {
		t.Errorf("rotation 45 should clamp to 0, got %d", opts.Pages[0].Rotation)
	}
	if opts.Pages[1].Rotation != 90 {
		t.Errorf("rotation 90 should survive, got %d", opts.Pages[1].Rotation)
	}
	// Out-of-range corners clamp into [0,1].
	c := opts.Pages[1].Corners
	if !c.complete() {
		t.Fatal("complete corners should survive")
	}
	if c.TL.X != 0 || c.TR.X != 1 {
		t.Errorf("corners should clamp: TL.X=%f TR.X=%f", c.TL.X, c.TR.X)
	}
	if opts.Pages[2].Corners != nil {
		t.Error("partial corners should be dropped")
	}
}

func TestParseScanOptionsLegacyOCROnSchemaMismatch(t *testing.T) {
	// pages as a non-array breaks the schema; legacy ocr flag must survive.
	opts := parseScanOptions(map[string]interface{}{
		"ocr":   true,
		"pages": "garbage",
	})
	if !opts.OCR {
		t.Error("legacy ocr flag should survive schema mismatch")
	}
}

func TestTesseractLang(t *testing.T) {
	cases := map[string]string{
		"eng": "eng", "deu": "deu", "fra": "fra", "spa": "spa", "hin": "hin",
		"":  "eng",
		"x": "eng", "ENG": "eng", " deu ": "deu",
	}
	for in, want := range cases {
		if got := tesseractLang(in); got != want {
			t.Errorf("tesseractLang(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestImportConfigForPageSize(t *testing.T) {
	for _, size := range []string{"", "auto", "AUTO", "weird"} {
		imp, err := importConfigForPageSize(size)
		if err != nil || imp != nil {
			t.Errorf("pageSize %q: want nil config, got %v (err %v)", size, imp, err)
		}
	}

	a4, err := importConfigForPageSize("a4")
	if err != nil || a4 == nil {
		t.Fatalf("a4 config: %v", err)
	}
	if a4.PageSize != "A4" || !a4.UserDim {
		t.Errorf("a4 config: %+v", a4)
	}

	letter, err := importConfigForPageSize("letter")
	if err != nil || letter == nil || letter.PageSize != "Letter" {
		t.Fatalf("letter config: %+v (err %v)", letter, err)
	}

	id, err := importConfigForPageSize("id")
	if err != nil || id == nil || id.PageDim == nil {
		t.Fatalf("id config: %+v (err %v)", id, err)
	}
	if id.PageDim.Width < 240 || id.PageDim.Width > 246 {
		t.Errorf("id width = %f, want ≈243pt", id.PageDim.Width)
	}
}

func TestNeedsPreprocessing(t *testing.T) {
	if needsPreprocessing(ScanOptions{}) {
		t.Error("plain options need no preprocessing")
	}
	if !needsPreprocessing(ScanOptions{Enhance: "bw"}) {
		t.Error("enhance requires preprocessing")
	}
	if !needsPreprocessing(ScanOptions{Pages: []ScanPage{{Rotation: 90}}}) {
		t.Error("rotation requires preprocessing")
	}
	p := imaging.Point{X: 0.1, Y: 0.1}
	q := imaging.Point{X: 0.9, Y: 0.1}
	r := imaging.Point{X: 0.9, Y: 0.9}
	s := imaging.Point{X: 0.1, Y: 0.9}
	if !needsPreprocessing(ScanOptions{Pages: []ScanPage{{Corners: &ScanCorners{TL: &p, TR: &q, BR: &r, BL: &s}}}}) {
		t.Error("corners require preprocessing")
	}
}

func writeTestPNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{200, 200, 190, 255})
		}
	}
	if err := imaging.EncodePNG(path, img); err != nil {
		t.Fatal(err)
	}
}

func TestPreprocessScanImagesEndToEnd(t *testing.T) {
	dir := t.TempDir()
	scratch := t.TempDir()

	in1 := filepath.Join(dir, "page1.png")
	in2 := filepath.Join(dir, "page2.png")
	writeTestPNG(t, in1, 200, 260)
	writeTestPNG(t, in2, 200, 260)

	tl := imaging.Point{X: 0.1, Y: 0.1}
	tr := imaging.Point{X: 0.9, Y: 0.12}
	br := imaging.Point{X: 0.88, Y: 0.9}
	bl := imaging.Point{X: 0.1, Y: 0.88}

	opts := ScanOptions{
		Enhance: "bw",
		Pages: []ScanPage{
			{Corners: &ScanCorners{TL: &tl, TR: &tr, BR: &br, BL: &bl}, Rotation: 90},
			{}, // no crop for page 2, but enhance still applies
		},
	}

	out, err := preprocessScanImages(context.Background(), []string{in1, in2}, opts, scratch)
	if err != nil {
		t.Fatalf("preprocess: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 outputs, got %d", len(out))
	}

	for i, p := range out {
		if !strings.HasSuffix(p, ".png") {
			t.Errorf("output %d: bw should encode PNG, got %s", i, p)
		}
		if _, err := os.Stat(p); err != nil {
			t.Errorf("output %d missing: %v", i, err)
		}
		img, _, err := imaging.DecodeFile(p)
		if err != nil {
			t.Fatalf("decode output %d: %v", i, err)
		}
		// Bilevel check on a sample of pixels.
		b := img.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y += 13 {
			for x := b.Min.X; x < b.Max.X; x += 13 {
				r, g, bl2, _ := img.At(x, y).RGBA()
				v := uint8(r >> 8)
				if (v != 0 && v != 255) || r != g || g != bl2 {
					t.Fatalf("output %d not bilevel at (%d,%d): %d", i, x, y, v)
				}
			}
		}
	}

	// Page 1 was cropped (0.8×0.78 of 200×260) then rotated 90° — its output
	// must be landscape (wider than tall).
	img1, _, _ := imaging.DecodeFile(out[0])
	if img1.Bounds().Dx() <= img1.Bounds().Dy() {
		t.Errorf("rotated page should be landscape, got %v", img1.Bounds())
	}
}

func TestPreprocessScanImagesPDFPassthrough(t *testing.T) {
	dir := t.TempDir()
	pdf := filepath.Join(dir, "doc.pdf")
	if err := os.WriteFile(pdf, []byte("%PDF-1.4 fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := preprocessScanImages(context.Background(), []string{pdf}, ScanOptions{Enhance: "bw"}, t.TempDir())
	if err != nil {
		t.Fatalf("preprocess: %v", err)
	}
	if out[0] != pdf {
		t.Errorf("pdf should pass through untouched, got %s", out[0])
	}
}

func TestPreprocessSkipsUntouchedPages(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "plain.png")
	writeTestPNG(t, in, 50, 50)

	// No enhance, no corners, no rotation → original path returned.
	out, err := preprocessScanImages(context.Background(), []string{in}, ScanOptions{Pages: []ScanPage{{}}}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if out[0] != in {
		t.Errorf("untouched page should keep original path, got %s", out[0])
	}
}

func TestScanToPDFPlainStillWorks(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "page.png")
	writeTestPNG(t, in, 100, 130)
	outPath := filepath.Join(dir, "out.pdf")

	if err := scanToPDF(context.Background(), []string{in}, outPath, nil); err != nil {
		t.Fatalf("plain scanToPDF: %v", err)
	}
	if fi, err := os.Stat(outPath); err != nil || fi.Size() == 0 {
		t.Fatalf("output pdf missing or empty: %v", err)
	}
}

func TestScanToPDFWithProcessing(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "page.png")
	writeTestPNG(t, in, 100, 130)
	outPath := filepath.Join(dir, "out.pdf")

	options := map[string]interface{}{
		"pageSize": "a4",
		"enhance":  "grayscale",
		"pages": []interface{}{
			map[string]interface{}{"rotation": 180},
		},
	}
	if err := scanToPDF(context.Background(), []string{in}, outPath, options); err != nil {
		t.Fatalf("processed scanToPDF: %v", err)
	}
	if fi, err := os.Stat(outPath); err != nil || fi.Size() == 0 {
		t.Fatalf("output pdf missing or empty: %v", err)
	}
}

package processing

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"path/filepath"
	"testing"

	"organize-pdf/internal/imaging"
)

// writePage writes a paper-grey PNG with optional painted regions.
func writePage(t *testing.T, path string, w, h int, paint func(*image.RGBA)) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 196, 194, 188, 255
	}
	if paint != nil {
		paint(img)
	}
	if err := imaging.EncodePNG(path, img); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupFlagsReachThePipelineAndGlareWarns(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "glossy.png")
	writePage(t, in, 300, 300, func(img *image.RGBA) {
		for y := 120; y < 180; y++ {
			for x := 120; x < 180; x++ {
				img.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
			}
		}
	})
	opts := ScanOptions{Pages: []ScanPage{{Cleanup: &ScanCleanup{Glare: true}}}}
	if !needsPreprocessing(opts) {
		t.Fatal("a clean-up flag alone must trigger preprocessing")
	}
	out, warnings, err := preprocessScanImages(context.Background(), []string{in}, opts, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if out[0] == in {
		t.Fatal("clean-up page was passed through undecoded")
	}
	if len(warnings) != 1 || warnings[0] != (ScanWarning{Page: 1, Code: "glare"}) {
		t.Errorf("warnings %+v, want one glare warning on page 1", warnings)
	}
	img, _, _ := imaging.DecodeFile(out[0])
	r, _, _, _ := img.At(150, 150).RGBA()
	if r>>8 > 210 {
		t.Errorf("glare not suppressed (r=%d)", r>>8)
	}
}

func TestEdgeMidsSelectTheCurvedWarp(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "book.png")
	writePage(t, in, 200, 260, nil)
	pt := func(x, y float64) *imaging.Point { return &imaging.Point{X: x, Y: y} }
	corners := &ScanCorners{TL: pt(0.1, 0.1), TR: pt(0.9, 0.1), BR: pt(0.9, 0.9), BL: pt(0.1, 0.9)}
	flat := ScanPage{Corners: corners}
	curved := ScanPage{Corners: corners, EdgeMids: &ScanEdgeMids{Top: pt(0.5, 0.2), Right: pt(0.9, 0.5), Bottom: pt(0.5, 0.95), Left: pt(0.1, 0.5)}}
	img, _, _ := imaging.DecodeFile(in)
	a, _, _, err := processOnePage(img, &flat, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	b, _, _, err := processOnePage(img, &curved, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Bowed top and bottom edges are longer than their chords → a wider page.
	if b.Bounds().Dx() <= a.Bounds().Dx() {
		t.Errorf("curved warp width %d should exceed planar %d", b.Bounds().Dx(), a.Bounds().Dx())
	}
	partial := ScanPage{Corners: corners, EdgeMids: &ScanEdgeMids{Top: pt(0.5, 0.2)}}
	c, _, _, _ := processOnePage(img, &partial, "", 0)
	if c.Bounds() != a.Bounds() {
		t.Error("incomplete edge midpoints must fall back to the planar warp")
	}
}

func TestCleanupOptionsParseFromJobOptions(t *testing.T) {
	raw := map[string]interface{}{}
	_ = json.Unmarshal([]byte(`{"pages":[{"cleanup":{"fingers":true,"holes":true}},{}]}`), &raw)
	opts := parseScanOptions(raw)
	if got := opts.Pages[0].cleanupOptions(); !got.Fingers || !got.Holes || got.Glare {
		t.Errorf("parsed %+v", got)
	}
	if opts.Pages[1].needsWork() {
		t.Error("a page without directives needs no work")
	}
	if withWarnings(nil, nil) != nil {
		t.Error("no warnings must leave metadata nil")
	}
	if m := withWarnings(map[string]interface{}{"extractedText": "x"}, []ScanWarning{{Page: 2, Code: "glare"}}); m["extractedText"] != "x" || m["warnings"] == nil {
		t.Errorf("merge %+v", m)
	}
}

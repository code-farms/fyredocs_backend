//go:build integration && binaries

// OCR overlay (doc 11 §5.2): the text layer is stamped onto the original page,
// so the page must look exactly as before and the invisible words must sit on
// the visible ones, including on a page the scanner stored sideways with
// /Rotate. The first engine benchmark (2026-11-16) measured the old rebuild at
// SSIM 0.86 against the input.
package processing

import (
	"context"
	"image"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"fyredocs/shared/jobslots"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// renderGray renders page 1 at 72 dpi in greyscale, so one pixel is one point.
func renderGray(t *testing.T, pdf string) *image.Gray {
	t.Helper()
	base := filepath.Join(t.TempDir(), "r")
	if out, err := exec.Command("pdftoppm", "-f", "1", "-l", "1", "-r", "72", "-gray", "-png", "-singlefile", pdf, base).CombinedOutput(); err != nil {
		t.Fatalf("pdftoppm %s: %v (%s)", pdf, err, out)
	}
	f, err := os.Open(base + ".png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if g, ok := img.(*image.Gray); ok {
		return g
	}
	g := image.NewGray(img.Bounds())
	draw.Draw(g, g.Bounds(), img, img.Bounds().Min, draw.Src)
	return g
}

// meanAbsDiff is the mean per-pixel difference of two equal-size renders.
func meanAbsDiff(t *testing.T, a, b *image.Gray) float64 {
	t.Helper()
	if a.Bounds() != b.Bounds() {
		t.Fatalf("render sizes differ: %v vs %v", a.Bounds(), b.Bounds())
	}
	var sum int
	for i := range a.Pix {
		d := int(a.Pix[i]) - int(b.Pix[i])
		if d < 0 {
			d = -d
		}
		sum += d
	}
	return float64(sum) / float64(len(a.Pix))
}

// inkBounds is the bounding box of dark pixels: where the visible text is.
func inkBounds(g *image.Gray) image.Rectangle {
	r := image.Rectangle{Min: g.Bounds().Max, Max: g.Bounds().Min}
	for y := g.Bounds().Min.Y; y < g.Bounds().Max.Y; y++ {
		for x := g.Bounds().Min.X; x < g.Bounds().Max.X; x++ {
			if g.GrayAt(x, y).Y < 128 {
				r = r.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return r
}

var bboxWordRE = regexp.MustCompile(`<word xMin="([\d.]+)" yMin="([\d.]+)" xMax="([\d.]+)" yMax="([\d.]+)">([^<]*)</word>`)

// wordCentre returns the centre, in displayed page points, of the first word
// on page 1 whose text is word.
func wordCentre(t *testing.T, pdf, word string) (image.Point, bool) {
	t.Helper()
	out, err := exec.Command("pdftotext", "-bbox", "-f", "1", "-l", "1", pdf, "-").Output()
	if err != nil {
		t.Fatalf("pdftotext -bbox: %v", err)
	}
	for _, m := range bboxWordRE.FindAllStringSubmatch(string(out), -1) {
		if m[5] != word {
			continue
		}
		v := func(s string) float64 { f, _ := strconv.ParseFloat(s, 64); return f }
		return image.Pt(int((v(m[1])+v(m[3]))/2), int((v(m[2])+v(m[4]))/2)), true
	}
	return image.Point{}, false
}

// rotatePNGCCW rewrites a PNG turned 90° counter-clockwise, the way a scanner
// stores a page it then marks /Rotate 90.
func rotatePNGCCW(t *testing.T, path string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	src, err := png.Decode(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dy(), b.Dx()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dst.Set(y-b.Min.Y, b.Max.X-1-x, src.At(x, y))
		}
	}
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if err := png.Encode(out, dst); err != nil {
		t.Fatal(err)
	}
}

func TestOCROverlayKeepsOriginalPageBinaries(t *testing.T) {
	for _, rotated := range []bool{false, true} {
		name := map[bool]string{false: "upright", true: "rotate-90"}[rotated]
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			img := filepath.Join(dir, "page.png")
			writeTextPNG(t, img, "HELLO OVERLAY WORLD", 6)
			if rotated {
				rotatePNGCCW(t, img)
			}
			input := filepath.Join(dir, "input.pdf")
			if err := api.ImportImagesFile(t.Context(), []string{img}, input, nil, nil); err != nil {
				t.Fatal(err)
			}
			if rotated {
				turned := filepath.Join(dir, "turned.pdf")
				if err := api.RotateFile(t.Context(), input, turned, 90, nil, nil); err != nil {
					t.Fatal(err)
				}
				input = turned
			}

			output := filepath.Join(dir, "out.pdf")
			meta, err := ocrPDF(context.Background(), input, output, map[string]interface{}{"language": "en"})
			if err != nil {
				t.Fatalf("ocrPDF: %v", err)
			}
			assertValidPDF(t, output)
			if meta["ocrMode"] != "overlay" {
				t.Fatalf("ocrMode = %v, want overlay", meta["ocrMode"])
			}

			// The page looks exactly as before: the scan is the original, not
			// a re-render, and the text layer is invisible.
			in, out := renderGray(t, input), renderGray(t, output)
			if d := meanAbsDiff(t, in, out); d > 0.5 {
				t.Errorf("page changed: mean pixel difference %.2f (want ≤ 0.5)", d)
			}

			// The invisible word sits on the visible text.
			centre, ok := wordCentre(t, output, "HELLO")
			if !ok {
				t.Fatalf("OCR text layer has no word HELLO")
			}
			ink := inkBounds(in).Inset(-6)
			if !centre.In(ink) {
				t.Errorf("text layer word at %v, visible text at %v: layer misplaced", centre, ink)
			}
		})
	}
}

// OCR pages draw cpu tokens from the worker's class slots (shared/jobslots),
// so all OCR jobs together run at most CPU_JOB_SLOTS tesseracts. With the only
// token held elsewhere the job must wait, and finish once it is released.
func TestOCRPagesDrawCPUTokensBinaries(t *testing.T) {
	dir := t.TempDir()
	input := buildImagePDF(t, dir, "TOKEN PAGE ONE", "TOKEN PAGE TWO")
	slots := jobslots.New("optimize-pdf", 1, 1)
	hold, err := slots.Acquire(context.Background(), jobslots.CPU, 1)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(jobslots.WithSlots(context.Background(), slots), 2*time.Second)
	defer cancel()
	if _, err := ocrPDF(ctx, input, filepath.Join(dir, "blocked.pdf"), map[string]interface{}{"language": "en"}); err == nil {
		t.Fatal("OCR ran although the only cpu token was held: pages do not draw tokens")
	}

	hold()
	ctx2 := jobslots.WithSlots(context.Background(), slots)
	if _, err := ocrPDF(ctx2, input, filepath.Join(dir, "ok.pdf"), map[string]interface{}{"language": "en"}); err != nil {
		t.Fatalf("OCR with a free token: %v", err)
	}
}

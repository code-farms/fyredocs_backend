package imaging

import (
	"image"
	"image/color"
	"math"
	"math/rand"
	"testing"
)

// syntheticDocument draws a bright quad on a dark canvas.
func syntheticDocument(w, h int, quad Quad) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// Dark background.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{30, 30, 35, 255})
		}
	}
	// Fill the quad via point-in-polygon (crossing number).
	px := quad.Denormalize(w, h)
	poly := []Point{px.TL, px.TR, px.BR, px.BL}
	inside := func(x, y float64) bool {
		crossings := 0
		for i := 0; i < 4; i++ {
			a, b := poly[i], poly[(i+1)%4]
			if (a.Y > y) != (b.Y > y) {
				xInt := a.X + (y-a.Y)/(b.Y-a.Y)*(b.X-a.X)
				if x < xInt {
					crossings++
				}
			}
		}
		return crossings%2 == 1
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if inside(float64(x)+0.5, float64(y)+0.5) {
				img.Set(x, y, color.RGBA{235, 235, 230, 255})
			}
		}
	}
	return img
}

func TestDetectDocumentQuadSynthetic(t *testing.T) {
	want := Quad{
		TL: Point{0.15, 0.12},
		TR: Point{0.88, 0.10},
		BR: Point{0.90, 0.88},
		BL: Point{0.12, 0.90},
	}
	img := syntheticDocument(400, 500, want)

	got, conf := DetectDocumentQuad(img)
	if conf < 0.5 {
		t.Fatalf("confidence %f too low for a clean synthetic document", conf)
	}

	check := func(g, w Point, label string) {
		if math.Abs(g.X-w.X) > 0.03 || math.Abs(g.Y-w.Y) > 0.03 {
			t.Errorf("%s: got (%.3f, %.3f), want (%.3f, %.3f) ±0.03", label, g.X, g.Y, w.X, w.Y)
		}
	}
	check(got.TL, want.TL, "TL")
	check(got.TR, want.TR, "TR")
	check(got.BR, want.BR, "BR")
	check(got.BL, want.BL, "BL")
}

func TestDetectDocumentQuadBlank(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 300, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 300; x++ {
			img.Set(x, y, color.RGBA{128, 128, 128, 255})
		}
	}
	quad, conf := DetectDocumentQuad(img)
	if conf != 0 {
		t.Errorf("blank image: confidence = %f, want 0", conf)
	}
	if quad != FullImageQuad() {
		t.Errorf("blank image: quad = %+v, want full-image fallback", quad)
	}
}

func TestDetectDocumentQuadNoise(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	img := image.NewRGBA(image.Rect(0, 0, 300, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 300; x++ {
			v := uint8(rng.Intn(256))
			img.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	// Pure noise: either fallback (confidence 0) or a low-confidence quad —
	// what matters is that it never panics and never claims high confidence.
	_, conf := DetectDocumentQuad(img)
	if conf > 0.7 {
		t.Errorf("noise image: confidence %f suspiciously high", conf)
	}
}

func TestDetectDocumentQuadTiny(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	quad, conf := DetectDocumentQuad(img)
	if conf != 0 || quad != FullImageQuad() {
		t.Error("tiny image should return full-image fallback with confidence 0")
	}
}

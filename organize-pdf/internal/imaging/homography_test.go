package imaging

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func TestComputeHomographyIdentity(t *testing.T) {
	pts := [4]Point{{0, 0}, {100, 0}, {100, 100}, {0, 100}}
	h, err := ComputeHomography(pts, pts)
	if err != nil {
		t.Fatalf("identity homography failed: %v", err)
	}
	for _, p := range []Point{{0, 0}, {50, 50}, {100, 100}, {25, 75}} {
		x, y := apply(h, p.X, p.Y)
		if math.Abs(x-p.X) > 1e-6 || math.Abs(y-p.Y) > 1e-6 {
			t.Errorf("identity mapped (%f,%f) to (%f,%f)", p.X, p.Y, x, y)
		}
	}
}

func TestComputeHomographyKnownTrapezoid(t *testing.T) {
	src := [4]Point{{10, 20}, {190, 10}, {180, 220}, {20, 230}}
	dst := [4]Point{{0, 0}, {200, 0}, {200, 250}, {0, 250}}
	h, err := ComputeHomography(src, dst)
	if err != nil {
		t.Fatalf("homography failed: %v", err)
	}
	for i := range src {
		x, y := apply(h, src[i].X, src[i].Y)
		if math.Abs(x-dst[i].X) > 1e-5 || math.Abs(y-dst[i].Y) > 1e-5 {
			t.Errorf("corner %d: mapped to (%f,%f), want (%f,%f)", i, x, y, dst[i].X, dst[i].Y)
		}
	}
}

func TestComputeHomographyDegenerate(t *testing.T) {
	// Three collinear source points → singular system.
	src := [4]Point{{0, 0}, {50, 0}, {100, 0}, {0, 100}}
	dst := [4]Point{{0, 0}, {100, 0}, {100, 100}, {0, 100}}
	if _, err := ComputeHomography(src, dst); err == nil {
		t.Error("expected error for collinear correspondence")
	}
}

func TestWarpOutputSize(t *testing.T) {
	quad := Quad{
		TL: Point{0, 0}, TR: Point{200, 0},
		BR: Point{200, 300}, BL: Point{0, 300},
	}
	w, h := WarpOutputSize(quad, 0)
	if w != 200 || h != 300 {
		t.Errorf("got %dx%d, want 200x300", w, h)
	}

	// Fixed aspect (ID card, w/h ≈ 1.586) overrides derived height.
	w, h = WarpOutputSize(quad, 85.6/54.0)
	wantH := int(math.Round(200 / (85.6 / 54.0)))
	if w != 200 || h != wantH {
		t.Errorf("aspect: got %dx%d, want 200x%d", w, h, wantH)
	}

	// Clamps.
	small := Quad{TL: Point{0, 0}, TR: Point{2, 0}, BR: Point{2, 2}, BL: Point{0, 2}}
	w, h = WarpOutputSize(small, 0)
	if w < 16 || h < 16 {
		t.Errorf("clamp floor violated: %dx%d", w, h)
	}
}

func TestWarpPerspectiveCornerColors(t *testing.T) {
	// A 100×100 image with distinct corner quadrant colors; warping the inner
	// quad that covers the whole image should preserve corner colors.
	src := image.NewRGBA(image.Rect(0, 0, 100, 100))
	quadrant := func(x, y int) color.RGBA {
		switch {
		case x < 50 && y < 50:
			return color.RGBA{255, 0, 0, 255} // TL red
		case x >= 50 && y < 50:
			return color.RGBA{0, 255, 0, 255} // TR green
		case x >= 50 && y >= 50:
			return color.RGBA{0, 0, 255, 255} // BR blue
		default:
			return color.RGBA{255, 255, 0, 255} // BL yellow
		}
	}
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			src.Set(x, y, quadrant(x, y))
		}
	}

	quad := Quad{TL: Point{0, 0}, TR: Point{99, 0}, BR: Point{99, 99}, BL: Point{0, 99}}
	out, err := WarpPerspective(src, quad, 100, 100)
	if err != nil {
		t.Fatalf("warp failed: %v", err)
	}

	check := func(x, y int, want color.RGBA, label string) {
		r, g, b, _ := out.At(x, y).RGBA()
		got := color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 255}
		if got != want {
			t.Errorf("%s corner: got %+v, want %+v", label, got, want)
		}
	}
	check(5, 5, color.RGBA{255, 0, 0, 255}, "TL")
	check(94, 5, color.RGBA{0, 255, 0, 255}, "TR")
	check(94, 94, color.RGBA{0, 0, 255, 255}, "BR")
	check(5, 94, color.RGBA{255, 255, 0, 255}, "BL")
}

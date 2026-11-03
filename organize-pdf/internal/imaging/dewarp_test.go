package imaging

import (
	"image/color"
	"math"
	"testing"
)

func TestCoonsReducesToCornersAndStraightEdges(t *testing.T) {
	t.Parallel()
	q := Quad{TL: Point{10, 20}, TR: Point{110, 20}, BR: Point{110, 220}, BL: Point{10, 220}}
	m := StraightMids(q)
	for _, c := range []struct{ u, v, x, y float64 }{
		{0, 0, 10, 20}, {1, 0, 110, 20}, {1, 1, 110, 220}, {0, 1, 10, 220}, {0.5, 0.5, 60, 120},
	} {
		x, y := coons(q, m, c.u, c.v)
		if math.Abs(x-c.x) > 1e-9 || math.Abs(y-c.y) > 1e-9 {
			t.Errorf("coons(%v,%v) = %v,%v want %v,%v", c.u, c.v, x, y, c.x, c.y)
		}
	}
	if b := bezier(Point{0, 0}, Point{5, 3}, Point{10, 0}, 0.5); b != (Point{5, 3}) {
		t.Errorf("curve must pass through its midpoint, got %v", b)
	}
}

// A page whose top and bottom edges bow downward by 30px; a text line drawn
// along the same bow halfway down must come out straight.
func TestCoonsWarpFlattensABowedLine(t *testing.T) {
	t.Parallel()
	const w, h = 300, 260
	src := page(w, h, paperGrey)
	q := Quad{TL: Point{20, 20}, TR: Point{280, 20}, BR: Point{280, 240}, BL: Point{20, 240}}
	m := StraightMids(q)
	m.Top.Y += 30
	m.Bottom.Y += 30
	for i := 0; i <= 600; i++ {
		x, y := coons(q, m, float64(i)/600, 0.5)
		for dy := -1.0; dy <= 1; dy++ {
			src.SetRGBA(int(math.Round(x)), int(math.Round(y+dy)), ink)
		}
	}
	ow, oh := CoonsOutputSize(q, m, 0)
	if ow <= 260 {
		t.Errorf("arc-length width %d should exceed the chord (260)", ow)
	}
	out, err := CoonsWarp(src, q, m, ow, oh)
	if err != nil {
		t.Fatal(err)
	}
	row := oh / 2
	dark := 0
	for x := 5; x < ow-5; x++ {
		if lumaAt(out, x, row) < 120 || lumaAt(out, x, row-1) < 120 || lumaAt(out, x, row+1) < 120 {
			dark++
		}
	}
	if float64(dark)/float64(ow-10) < 0.95 {
		t.Errorf("only %d/%d columns dark on the middle row — the bowed line was not flattened", dark, ow-10)
	}
	if _, err := CoonsWarp(src, q, m, 0, 10); err == nil {
		t.Error("empty output must error")
	}
}

func TestCoonsOutputSizeAspect(t *testing.T) {
	t.Parallel()
	q := Quad{TL: Point{0, 0}, TR: Point{200, 0}, BR: Point{200, 100}, BL: Point{0, 100}}
	w, h := CoonsOutputSize(q, StraightMids(q), 2)
	if w != 200 || h != 100 {
		t.Errorf("got %dx%d", w, h)
	}
}

// A light page on a dark table whose top edge bows: detection must move the
// top midpoint toward the bow and leave the straight edges alone.
func TestDetectEdgeMidpoints(t *testing.T) {
	t.Parallel()
	const w, h = 400, 500
	img := page(w, h, color.RGBA{40, 40, 45, 255})
	q := Quad{TL: Point{60, 60}, TR: Point{340, 60}, BR: Point{340, 440}, BL: Point{60, 440}}
	m := StraightMids(q)
	m.Top.Y += 35 // the page's top edge sags 35px in the middle
	for y := 0; y < h; y++ {
		for x := 60; x <= 340; x++ {
			u := float64(x-60) / 280
			top := bezier(q.TL, m.Top, q.TR, u).Y
			if float64(y) >= top && y <= 440 {
				img.SetRGBA(x, y, paperGrey)
			}
		}
	}
	mids, curv := DetectEdgeMidpoints(img, q.Normalize(w, h))
	got := mids.Denormalize(w, h)
	if math.Abs(got.Top.Y-95) > 10 {
		t.Errorf("top midpoint y = %.1f, want ≈95 (the sag)", got.Top.Y)
	}
	if math.Abs(got.Bottom.Y-440) > 4 || math.Abs(got.Left.X-60) > 4 || math.Abs(got.Right.X-340) > 4 {
		t.Errorf("straight edges moved: %+v", got)
	}
	if curv < 0.08 {
		t.Errorf("curvature %.3f, want ≈0.125", curv)
	}

	flat := page(w, h, color.RGBA{40, 40, 45, 255})
	for y := 60; y <= 440; y++ {
		for x := 60; x <= 340; x++ {
			flat.SetRGBA(x, y, paperGrey)
		}
	}
	if _, c := DetectEdgeMidpoints(flat, q.Normalize(w, h)); c != 0 {
		t.Errorf("flat page curvature %.3f, want 0", c)
	}
}

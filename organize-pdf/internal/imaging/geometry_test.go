package imaging

import (
	"math"
	"testing"
)

func TestOrderCornersPermutations(t *testing.T) {
	want := Quad{
		TL: Point{0.1, 0.1},
		TR: Point{0.9, 0.15},
		BR: Point{0.85, 0.9},
		BL: Point{0.12, 0.88},
	}
	perms := [][4]Point{
		{want.TL, want.TR, want.BR, want.BL},
		{want.BR, want.TL, want.BL, want.TR},
		{want.BL, want.BR, want.TR, want.TL},
		{want.TR, want.BL, want.TL, want.BR},
	}
	for i, pts := range perms {
		got := OrderCorners(pts)
		if got != want {
			t.Errorf("perm %d: got %+v, want %+v", i, got, want)
		}
	}
}

func TestQuadAreaUnitSquare(t *testing.T) {
	q := FullImageQuad()
	if area := q.Area(); math.Abs(area-1) > 1e-9 {
		t.Errorf("full-image quad area = %f, want 1", area)
	}
}

func TestQuadConvexity(t *testing.T) {
	if !FullImageQuad().IsConvex() {
		t.Error("unit square should be convex")
	}
	// Bowtie (self-intersecting) — TR and BR swapped.
	bowtie := Quad{
		TL: Point{0, 0},
		TR: Point{1, 1},
		BR: Point{1, 0},
		BL: Point{0, 1},
	}
	if bowtie.IsConvex() {
		t.Error("bowtie quad should not be convex")
	}
}

func TestQuadValidate(t *testing.T) {
	if err := FullImageQuad().Validate(); err != nil {
		t.Errorf("full-image quad should validate: %v", err)
	}

	tiny := Quad{
		TL: Point{0.4, 0.4}, TR: Point{0.45, 0.4},
		BR: Point{0.45, 0.45}, BL: Point{0.4, 0.45},
	}
	if err := tiny.Validate(); err == nil {
		t.Error("tiny quad should fail validation")
	}

	outside := Quad{
		TL: Point{-0.5, 0}, TR: Point{1, 0},
		BR: Point{1, 1}, BL: Point{0, 1},
	}
	if err := outside.Validate(); err == nil {
		t.Error("out-of-bounds quad should fail validation")
	}

	// Collinear corners (zero-area edge) must fail.
	collinear := Quad{
		TL: Point{0, 0}, TR: Point{0.5, 0},
		BR: Point{1, 0}, BL: Point{0, 1},
	}
	if err := collinear.Validate(); err == nil {
		t.Error("collinear quad should fail validation")
	}
}

func TestNormalizeDenormalizeRoundTrip(t *testing.T) {
	q := Quad{
		TL: Point{0.1, 0.2}, TR: Point{0.9, 0.25},
		BR: Point{0.88, 0.9}, BL: Point{0.12, 0.85},
	}
	rt := q.Denormalize(640, 480).Normalize(640, 480)
	pts, want := rt.corners(), q.corners()
	for i := range pts {
		if math.Abs(pts[i].X-want[i].X) > 1e-9 || math.Abs(pts[i].Y-want[i].Y) > 1e-9 {
			t.Fatalf("corner %d: got %+v, want %+v", i, pts[i], want[i])
		}
	}
}

func TestClamp01(t *testing.T) {
	q := Quad{
		TL: Point{-0.2, 0.5}, TR: Point{1.3, -0.1},
		BR: Point{0.5, 1.7}, BL: Point{0.5, 0.5},
	}
	c := q.Clamp01()
	for _, p := range c.corners() {
		if p.X < 0 || p.X > 1 || p.Y < 0 || p.Y > 1 {
			t.Fatalf("clamped point %+v outside [0,1]", p)
		}
	}
}

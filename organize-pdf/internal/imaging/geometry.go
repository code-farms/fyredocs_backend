// Package imaging implements the pure-Go image processing used by the
// document scanner: quad geometry, perspective warping, enhancement filters,
// and document edge detection. Everything here is CGO-free by design — the
// service builds with CGO_ENABLED=0.
package imaging

import (
	"fmt"
	"math"
)

// Point is a 2D coordinate. Depending on context it is either normalized
// (0..1 relative to image dimensions) or in pixel space.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Quad is a document quadrilateral with named corners in clockwise order
// starting top-left.
type Quad struct {
	TL Point `json:"tl"`
	TR Point `json:"tr"`
	BR Point `json:"br"`
	BL Point `json:"bl"`
}

// FullImageQuad returns the normalized quad covering the entire image — the
// graceful fallback when detection finds nothing.
func FullImageQuad() Quad {
	return Quad{
		TL: Point{0, 0},
		TR: Point{1, 0},
		BR: Point{1, 1},
		BL: Point{0, 1},
	}
}

// Denormalize converts 0..1 coordinates to pixel coordinates for a w×h image.
func (q Quad) Denormalize(w, h int) Quad {
	fw, fh := float64(w), float64(h)
	return Quad{
		TL: Point{q.TL.X * fw, q.TL.Y * fh},
		TR: Point{q.TR.X * fw, q.TR.Y * fh},
		BR: Point{q.BR.X * fw, q.BR.Y * fh},
		BL: Point{q.BL.X * fw, q.BL.Y * fh},
	}
}

// Normalize converts pixel coordinates to 0..1 for a w×h image.
func (q Quad) Normalize(w, h int) Quad {
	if w <= 0 || h <= 0 {
		return q
	}
	fw, fh := float64(w), float64(h)
	return Quad{
		TL: Point{q.TL.X / fw, q.TL.Y / fh},
		TR: Point{q.TR.X / fw, q.TR.Y / fh},
		BR: Point{q.BR.X / fw, q.BR.Y / fh},
		BL: Point{q.BL.X / fw, q.BL.Y / fh},
	}
}

// Clamp01 clamps every coordinate into [0, 1].
func (q Quad) Clamp01() Quad {
	clamp := func(v float64) float64 {
		if v < 0 {
			return 0
		}
		if v > 1 {
			return 1
		}
		return v
	}
	cp := func(p Point) Point { return Point{clamp(p.X), clamp(p.Y)} }
	return Quad{TL: cp(q.TL), TR: cp(q.TR), BR: cp(q.BR), BL: cp(q.BL)}
}

func (q Quad) corners() [4]Point {
	return [4]Point{q.TL, q.TR, q.BR, q.BL}
}

// Area computes the quad's area via the shoelace formula (absolute value, so
// winding order does not matter).
func (q Quad) Area() float64 {
	pts := q.corners()
	sum := 0.0
	for i := 0; i < 4; i++ {
		j := (i + 1) % 4
		sum += pts[i].X*pts[j].Y - pts[j].X*pts[i].Y
	}
	return math.Abs(sum) / 2
}

// IsConvex reports whether the quad's corners (in TL→TR→BR→BL order) form a
// convex polygon: every cross product of consecutive edges has the same sign.
func (q Quad) IsConvex() bool {
	pts := q.corners()
	sign := 0
	for i := 0; i < 4; i++ {
		a, b, c := pts[i], pts[(i+1)%4], pts[(i+2)%4]
		cross := (b.X-a.X)*(c.Y-b.Y) - (b.Y-a.Y)*(c.X-b.X)
		if math.Abs(cross) < 1e-12 {
			return false // collinear corners — degenerate
		}
		s := 1
		if cross < 0 {
			s = -1
		}
		if sign == 0 {
			sign = s
		} else if s != sign {
			return false
		}
	}
	return true
}

// cornerAngles returns each interior corner angle in degrees.
func (q Quad) cornerAngles() [4]float64 {
	pts := q.corners()
	var angles [4]float64
	for i := 0; i < 4; i++ {
		prev, cur, next := pts[(i+3)%4], pts[i], pts[(i+1)%4]
		v1 := Point{prev.X - cur.X, prev.Y - cur.Y}
		v2 := Point{next.X - cur.X, next.Y - cur.Y}
		dot := v1.X*v2.X + v1.Y*v2.Y
		m1 := math.Hypot(v1.X, v1.Y)
		m2 := math.Hypot(v2.X, v2.Y)
		if m1 == 0 || m2 == 0 {
			angles[i] = 0
			continue
		}
		cos := dot / (m1 * m2)
		cos = math.Max(-1, math.Min(1, cos))
		angles[i] = math.Acos(cos) * 180 / math.Pi
	}
	return angles
}

// Validate checks a normalized quad is usable for warping: convex, corners
// near the unit square, meaningful area, and no pathological corner angles.
func (q Quad) Validate() error {
	for _, p := range q.corners() {
		if p.X < -0.05 || p.X > 1.05 || p.Y < -0.05 || p.Y > 1.05 {
			return fmt.Errorf("imaging: corner (%.3f, %.3f) outside image bounds", p.X, p.Y)
		}
	}
	if !q.IsConvex() {
		return fmt.Errorf("imaging: quad is not convex")
	}
	if area := q.Area(); area < 0.05 {
		return fmt.Errorf("imaging: quad area %.3f too small", area)
	}
	for _, a := range q.cornerAngles() {
		if a < 30 || a > 150 {
			return fmt.Errorf("imaging: corner angle %.1f° out of range [30, 150]", a)
		}
	}
	return nil
}

// OrderCorners assigns four arbitrary points to TL/TR/BR/BL using the
// classic sum/diff heuristic: TL has the smallest x+y, BR the largest x+y,
// TR the largest x−y, BL the smallest x−y.
func OrderCorners(pts [4]Point) Quad {
	var q Quad
	minSum, maxSum := math.Inf(1), math.Inf(-1)
	minDiff, maxDiff := math.Inf(1), math.Inf(-1)
	for _, p := range pts {
		sum, diff := p.X+p.Y, p.X-p.Y
		if sum < minSum {
			minSum = sum
			q.TL = p
		}
		if sum > maxSum {
			maxSum = sum
			q.BR = p
		}
		if diff > maxDiff {
			maxDiff = diff
			q.TR = p
		}
		if diff < minDiff {
			minDiff = diff
			q.BL = p
		}
	}
	return q
}

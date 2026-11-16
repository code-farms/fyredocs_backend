package diff

import "math"

// PageSize is a page's width and height in PDF points.
type PageSize struct{ W, H float64 }

// dimTolerance is the largest per-side difference, in points, still treated as
// the same page size (doc 16 §5.6: abs diff ≤ 0.5 pt). Writers round box
// coordinates differently, so exact equality would flag harmless rewrites.
const dimTolerance = 0.5

// SameSize reports whether a and b are equal within dimTolerance.
func SameSize(a, b PageSize) bool {
	return math.Abs(a.W-b.W) <= dimTolerance && math.Abs(a.H-b.H) <= dimTolerance
}

// Rotated returns s turned by a quarter turn (width and height swapped), the
// size a rotate-pdf output page must have for a 90° or 270° rotation.
func Rotated(s PageSize) PageSize { return PageSize{W: s.H, H: s.W} }

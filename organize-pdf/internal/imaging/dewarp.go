package imaging

import (
	"errors"
	"image"
	"math"
)

// EdgeMids are the midpoints of a page's four edges (top, right, bottom,
// left). With the quad's corners they define a curved boundary: each edge
// becomes the quadratic Bézier through its two corners and its midpoint.
type EdgeMids struct {
	Top    Point `json:"top"`
	Right  Point `json:"right"`
	Bottom Point `json:"bottom"`
	Left   Point `json:"left"`
}

// Denormalize scales normalized midpoints to pixel space.
func (m EdgeMids) Denormalize(w, h int) EdgeMids {
	s := func(p Point) Point { return Point{p.X * float64(w-1), p.Y * float64(h-1)} }
	return EdgeMids{s(m.Top), s(m.Right), s(m.Bottom), s(m.Left)}
}

// Normalize scales pixel-space midpoints to 0..1.
func (m EdgeMids) Normalize(w, h int) EdgeMids {
	s := func(p Point) Point { return Point{p.X / float64(w-1), p.Y / float64(h-1)} }
	return EdgeMids{s(m.Top), s(m.Right), s(m.Bottom), s(m.Left)}
}

// StraightMids returns the midpoints of a quad's straight edges — the
// boundary that makes CoonsWarp reduce to a bilinear (near-planar) warp.
func StraightMids(q Quad) EdgeMids {
	mid := func(a, b Point) Point { return Point{(a.X + b.X) / 2, (a.Y + b.Y) / 2} }
	return EdgeMids{mid(q.TL, q.TR), mid(q.TR, q.BR), mid(q.BL, q.BR), mid(q.TL, q.BL)}
}

// bezier evaluates the quadratic Bézier from a to b that passes through mid
// at t = 0.5 (control point c = 2·mid − (a+b)/2).
func bezier(a, mid, b Point, t float64) Point {
	cx := 2*mid.X - (a.X+b.X)/2
	cy := 2*mid.Y - (a.Y+b.Y)/2
	u := 1 - t
	return Point{u*u*a.X + 2*u*t*cx + t*t*b.X, u*u*a.Y + 2*u*t*cy + t*t*b.Y}
}

// curveLength approximates a boundary curve's arc length by sampling.
func curveLength(a, mid, b Point) float64 {
	const n = 32
	prev, total := a, 0.0
	for i := 1; i <= n; i++ {
		p := bezier(a, mid, b, float64(i)/n)
		total += math.Hypot(p.X-prev.X, p.Y-prev.Y)
		prev = p
	}
	return total
}

// CoonsOutputSize picks the flattened page size from the boundary's arc
// lengths (curved edges are longer than their chords). aspect > 0 forces a
// fixed width/height ratio, as WarpOutputSize does for card sizes.
func CoonsOutputSize(q Quad, m EdgeMids, aspect float64) (int, int) {
	w := math.Max(curveLength(q.TL, m.Top, q.TR), curveLength(q.BL, m.Bottom, q.BR))
	h := math.Max(curveLength(q.TL, m.Left, q.BL), curveLength(q.TR, m.Right, q.BR))
	if aspect > 0 {
		h = w / aspect
	}
	return max(1, int(math.Round(w))), max(1, int(math.Round(h)))
}

// coons maps output (u, v) ∈ [0,1]² onto the source via the bilinearly
// blended Coons patch of the four boundary curves.
func coons(q Quad, m EdgeMids, u, v float64) (float64, float64) {
	top := bezier(q.TL, m.Top, q.TR, u)
	bot := bezier(q.BL, m.Bottom, q.BR, u)
	left := bezier(q.TL, m.Left, q.BL, v)
	right := bezier(q.TR, m.Right, q.BR, v)
	x := (1-v)*top.X + v*bot.X + (1-u)*left.X + u*right.X -
		((1-u)*(1-v)*q.TL.X + u*(1-v)*q.TR.X + (1-u)*v*q.BL.X + u*v*q.BR.X)
	y := (1-v)*top.Y + v*bot.Y + (1-u)*left.Y + u*right.Y -
		((1-u)*(1-v)*q.TL.Y + u*(1-v)*q.TR.Y + (1-u)*v*q.BL.Y + u*v*q.BR.Y)
	return x, y
}

// CoonsWarp flattens a curved page (a book spread, a rolled receipt): every
// output pixel samples the source at the Coons-patch position of its (u, v),
// bilinearly. Text lines between the curved top and bottom edges are
// interpolated between them — the classical boundary-based dewarp; pages
// whose curvature varies sharply mid-page are only approximated. q and m are
// in PIXEL space.
func CoonsWarp(src image.Image, q Quad, m EdgeMids, outW, outH int) (*image.RGBA, error) {
	if outW < 1 || outH < 1 {
		return nil, errors.New("imaging: empty dewarp output")
	}
	rgba := toRGBA(src)
	sw, sh := rgba.Bounds().Dx(), rgba.Bounds().Dy()
	out := image.NewRGBA(image.Rect(0, 0, outW, outH))
	du := 1.0 / math.Max(1, float64(outW-1))
	dv := 1.0 / math.Max(1, float64(outH-1))
	for y := 0; y < outH; y++ {
		for x := 0; x < outW; x++ {
			sx, sy := coons(q, m, float64(x)*du, float64(y)*dv)
			r, g, b, a := sampleBilinear(rgba, sw, sh, sx, sy)
			i := y*out.Stride + x*4
			out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = r, g, b, a
		}
	}
	return out, nil
}

// sampleBilinear samples rgba at a fractional position, clamped to its edges.
func sampleBilinear(rgba *image.RGBA, sw, sh int, sx, sy float64) (uint8, uint8, uint8, uint8) {
	sx = math.Max(0, math.Min(float64(sw-1), sx))
	sy = math.Max(0, math.Min(float64(sh-1), sy))
	x0, y0 := int(sx), int(sy)
	x1, y1 := min(x0+1, sw-1), min(y0+1, sh-1)
	fx, fy := sx-float64(x0), sy-float64(y0)
	var px [4]uint8
	for c := 0; c < 4; c++ {
		p00 := float64(rgba.Pix[y0*rgba.Stride+x0*4+c])
		p10 := float64(rgba.Pix[y0*rgba.Stride+x1*4+c])
		p01 := float64(rgba.Pix[y1*rgba.Stride+x0*4+c])
		p11 := float64(rgba.Pix[y1*rgba.Stride+x1*4+c])
		v := p00*(1-fx)*(1-fy) + p10*fx*(1-fy) + p01*(1-fx)*fy + p11*fx*fy
		px[c] = uint8(math.Round(v))
	}
	return px[0], px[1], px[2], px[3]
}

// Midpoint search tuning: offsets are searched along each edge's normal up
// to midSearchRatio of the edge length; a bow is accepted only when the
// curved path's edge response beats the straight one by midMinGain AND the
// bow is at least midMinBow of the edge — so flat pages stay flat. midMinBow
// sits above the 1–2% that a detected quad's own pixel error produces on a
// flat page (measured: 0.02), and below real book-spread bows (5–15%).
const (
	midSearchRatio = 0.12
	midSearchSteps = 24
	midMinGain     = 1.15
	midMinBow      = 0.03
)

// DetectEdgeMidpoints proposes curved edge midpoints for a detected
// (normalized) quad: for each edge it slides the midpoint along the edge
// normal and keeps the offset whose Bézier path best follows the Sobel edge
// map. Returns normalized midpoints and the largest bow as a fraction of its
// edge length (0 = flat). Straight midpoints come back for flat edges.
func DetectEdgeMidpoints(src image.Image, quad Quad) (EdgeMids, float64) {
	small, _ := DownscaleTo(src, detectMaxDim)
	b := small.Bounds()
	w, h := b.Dx(), b.Dy()
	mag := sobelMagnitude(gaussianBlur(Grayscale(small)))
	q := quad.Denormalize(w, h)
	straight := StraightMids(q)

	score := func(a, mid, c Point) float64 {
		total := 0.0
		for i := 1; i < 20; i++ {
			p := bezier(a, mid, c, float64(i)/20)
			x, y := int(math.Round(p.X)), int(math.Round(p.Y))
			if x >= 0 && y >= 0 && x < w && y < h {
				total += float64(mag[y*w+x])
			}
		}
		return total
	}
	bestBow := 0.0
	fit := func(a, c, mid Point) Point {
		dx, dy := c.X-a.X, c.Y-a.Y
		length := math.Hypot(dx, dy)
		if length < 1 {
			return mid
		}
		nx, ny := -dy/length, dx/length
		base := score(a, mid, c)
		best, bestOff := base, 0.0
		limit := length * midSearchRatio
		for i := -midSearchSteps; i <= midSearchSteps; i++ {
			off := limit * float64(i) / midSearchSteps
			if s := score(a, Point{mid.X + nx*off, mid.Y + ny*off}, c); s > best {
				best, bestOff = s, off
			}
		}
		if best < base*midMinGain || math.Abs(bestOff) < length*midMinBow {
			return mid
		}
		bestBow = math.Max(bestBow, math.Abs(bestOff)/length)
		return Point{mid.X + nx*bestOff, mid.Y + ny*bestOff}
	}
	mids := EdgeMids{
		Top:    fit(q.TL, q.TR, straight.Top),
		Right:  fit(q.TR, q.BR, straight.Right),
		Bottom: fit(q.BL, q.BR, straight.Bottom),
		Left:   fit(q.TL, q.BL, straight.Left),
	}
	return mids.Normalize(w, h), bestBow
}

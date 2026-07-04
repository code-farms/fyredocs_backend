package imaging

import (
	"fmt"
	"image"
	"image/draw"
	"math"
)

// ComputeHomography solves the 8-DOF projective transform h mapping each
// src[i] to dst[i] (h[8] fixed to 1). The 8×8 linear system is solved with
// Gaussian elimination and partial pivoting. Returns an error when the
// correspondence is degenerate (e.g. three collinear points).
func ComputeHomography(src, dst [4]Point) ([9]float64, error) {
	// Build A·x = b where x = [h0..h7].
	var a [8][9]float64 // augmented matrix, last column = b
	for i := 0; i < 4; i++ {
		sx, sy := src[i].X, src[i].Y
		dx, dy := dst[i].X, dst[i].Y
		a[2*i] = [9]float64{sx, sy, 1, 0, 0, 0, -sx * dx, -sy * dx, dx}
		a[2*i+1] = [9]float64{0, 0, 0, sx, sy, 1, -sx * dy, -sy * dy, dy}
	}

	// Gaussian elimination with partial pivoting.
	for col := 0; col < 8; col++ {
		pivot := col
		for row := col + 1; row < 8; row++ {
			if math.Abs(a[row][col]) > math.Abs(a[pivot][col]) {
				pivot = row
			}
		}
		if math.Abs(a[pivot][col]) < 1e-10 {
			return [9]float64{}, fmt.Errorf("imaging: degenerate correspondence (singular system)")
		}
		a[col], a[pivot] = a[pivot], a[col]
		for row := col + 1; row < 8; row++ {
			f := a[row][col] / a[col][col]
			for k := col; k < 9; k++ {
				a[row][k] -= f * a[col][k]
			}
		}
	}

	// Back substitution.
	var x [8]float64
	for row := 7; row >= 0; row-- {
		sum := a[row][8]
		for k := row + 1; k < 8; k++ {
			sum -= a[row][k] * x[k]
		}
		x[row] = sum / a[row][row]
	}

	return [9]float64{x[0], x[1], x[2], x[3], x[4], x[5], x[6], x[7], 1}, nil
}

// apply maps (x, y) through homography h.
func apply(h [9]float64, x, y float64) (float64, float64) {
	w := h[6]*x + h[7]*y + h[8]
	if math.Abs(w) < 1e-12 {
		return 0, 0
	}
	return (h[0]*x + h[1]*y + h[2]) / w, (h[3]*x + h[4]*y + h[5]) / w
}

// WarpOutputSize derives the warped output dimensions from a pixel-space
// quad: width = the longer of the two horizontal edges, height = the longer
// of the two vertical edges, clamped to [16, 6000]. When aspect > 0 (a known
// physical page ratio, width/height), height is recomputed as width/aspect.
func WarpOutputSize(quad Quad, aspect float64) (int, int) {
	dist := func(a, b Point) float64 { return math.Hypot(b.X-a.X, b.Y-a.Y) }
	w := math.Max(dist(quad.TL, quad.TR), dist(quad.BL, quad.BR))
	h := math.Max(dist(quad.TL, quad.BL), dist(quad.TR, quad.BR))
	if aspect > 0 {
		h = w / aspect
	}
	clamp := func(v float64) int {
		if v < 16 {
			return 16
		}
		if v > 6000 {
			return 6000
		}
		return int(math.Round(v))
	}
	return clamp(w), clamp(h)
}

// WarpPerspective maps the source quad (pixel coordinates) onto an upright
// outW×outH rectangle. It computes the inverse mapping (destination →
// source) directly and samples the source bilinearly; out-of-bounds samples
// clamp to the nearest edge pixel.
func WarpPerspective(src image.Image, quad Quad, outW, outH int) (*image.RGBA, error) {
	dst := [4]Point{
		{0, 0},
		{float64(outW - 1), 0},
		{float64(outW - 1), float64(outH - 1)},
		{0, float64(outH - 1)},
	}
	srcPts := [4]Point{quad.TL, quad.TR, quad.BR, quad.BL}

	// Inverse homography: destination → source, computed directly.
	hInv, err := ComputeHomography(dst, srcPts)
	if err != nil {
		return nil, err
	}

	// One-time conversion to RGBA for fast pixel access.
	b := src.Bounds()
	rgba, ok := src.(*image.RGBA)
	if !ok {
		rgba = image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Src)
		b = rgba.Bounds()
	}
	sw, sh := b.Dx(), b.Dy()

	out := image.NewRGBA(image.Rect(0, 0, outW, outH))
	for y := 0; y < outH; y++ {
		for x := 0; x < outW; x++ {
			sx, sy := apply(hInv, float64(x), float64(y))

			// Clamp to valid sample range.
			if sx < 0 {
				sx = 0
			} else if sx > float64(sw-1) {
				sx = float64(sw - 1)
			}
			if sy < 0 {
				sy = 0
			} else if sy > float64(sh-1) {
				sy = float64(sh - 1)
			}

			x0, y0 := int(sx), int(sy)
			x1, y1 := x0+1, y0+1
			if x1 > sw-1 {
				x1 = sw - 1
			}
			if y1 > sh-1 {
				y1 = sh - 1
			}
			fx, fy := sx-float64(x0), sy-float64(y0)

			i00 := rgba.PixOffset(b.Min.X+x0, b.Min.Y+y0)
			i10 := rgba.PixOffset(b.Min.X+x1, b.Min.Y+y0)
			i01 := rgba.PixOffset(b.Min.X+x0, b.Min.Y+y1)
			i11 := rgba.PixOffset(b.Min.X+x1, b.Min.Y+y1)

			oi := out.PixOffset(x, y)
			for c := 0; c < 4; c++ {
				top := float64(rgba.Pix[i00+c])*(1-fx) + float64(rgba.Pix[i10+c])*fx
				bot := float64(rgba.Pix[i01+c])*(1-fx) + float64(rgba.Pix[i11+c])*fx
				out.Pix[oi+c] = uint8(math.Round(top*(1-fy) + bot*fy))
			}
		}
	}
	return out, nil
}

package imaging

import (
	"image"

	xdraw "golang.org/x/image/draw"
)

// DownscaleTo scales src so its longest side is at most maxDim, preserving
// aspect ratio, and returns the scale factor applied (1.0 when no resize was
// needed). Uses approximate bilinear interpolation — plenty for detection.
func DownscaleTo(src image.Image, maxDim int) (*image.RGBA, float64) {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()

	longest := w
	if h > longest {
		longest = h
	}
	if longest <= maxDim || maxDim <= 0 {
		// Normalize to *image.RGBA without resizing.
		if rgba, ok := src.(*image.RGBA); ok && b.Min == (image.Point{}) {
			return rgba, 1
		}
		out := image.NewRGBA(image.Rect(0, 0, w, h))
		xdraw.Draw(out, out.Bounds(), src, b.Min, xdraw.Src)
		return out, 1
	}

	scale := float64(maxDim) / float64(longest)
	nw := int(float64(w) * scale)
	nh := int(float64(h) * scale)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}

	out := image.NewRGBA(image.Rect(0, 0, nw, nh))
	xdraw.ApproxBiLinear.Scale(out, out.Bounds(), src, b, xdraw.Src, nil)
	return out, scale
}

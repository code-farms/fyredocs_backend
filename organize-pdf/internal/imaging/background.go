package imaging

import (
	"image"
	"image/draw"
	"sort"

	xdraw "golang.org/x/image/draw"
)

// Background-estimation tuning. The estimate runs at a reduced working
// resolution: paper illumination is low-frequency, so nothing is lost, and a
// 12 MP photo costs the same as a 0.2 MP one.
const (
	bgWorkMaxDim = 512
	// bgRadiusDiv sets the max-filter radius as longest/bgRadiusDiv. At 512px
	// that is ~13px — wider than a text line at that scale, so the max filter
	// erases ink and leaves only the paper's lighting.
	bgRadiusDiv = 40
)

// toRGBA returns a fresh *image.RGBA copy of src with origin (0,0). Every
// clean-up returns a new image; the caller's image is never mutated.
func toRGBA(src image.Image) *image.RGBA {
	b := src.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), src, b.Min, draw.Src)
	return out
}

// EstimateBackground returns the paper's illumination at src's full size:
// a per-channel max filter (grey dilation) removes dark ink, a box blur
// smooths the result, and a bilinear upsample restores the size. Unlike a
// plain blur (the on-device flat field), the max filter is not darkened by
// dense text, so shadows divide out without washing text grey.
func EstimateBackground(src image.Image) *image.RGBA {
	small, _ := DownscaleTo(src, bgWorkMaxDim)
	sb := small.Bounds()
	w, h := sb.Dx(), sb.Dy()
	r := max(2, max(w, h)/bgRadiusDiv)

	work := make([]uint8, len(small.Pix))
	copy(work, small.Pix)
	tmp := make([]uint8, len(work))
	maxFilter(work, tmp, w, h, r)
	boxBlur(work, tmp, w, h, r)

	est := &image.RGBA{Pix: work, Stride: w * 4, Rect: image.Rect(0, 0, w, h)}
	b := src.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	xdraw.ApproxBiLinear.Scale(out, out.Bounds(), est, est.Bounds(), xdraw.Src, nil)
	return out
}

// maxFilter applies a separable (2r+1)² max filter to the RGB channels of an
// RGBA buffer in place (tmp is scratch of the same size).
func maxFilter(pix, tmp []uint8, w, h, r int) {
	for pass := 0; pass < 2; pass++ {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				for c := 0; c < 3; c++ {
					var m uint8
					for d := -r; d <= r; d++ {
						xx, yy := x, y
						if pass == 0 {
							xx = clampInt(x+d, 0, w-1)
						} else {
							yy = clampInt(y+d, 0, h-1)
						}
						if v := pix[(yy*w+xx)*4+c]; v > m {
							m = v
						}
					}
					tmp[(y*w+x)*4+c] = m
				}
				tmp[(y*w+x)*4+3] = 255
			}
		}
		copy(pix, tmp)
	}
}

// boxBlur applies a separable (2r+1)² mean filter with sliding sums, in place.
func boxBlur(pix, tmp []uint8, w, h, r int) {
	for pass := 0; pass < 2; pass++ {
		n := w
		lines := h
		if pass == 1 {
			n, lines = h, w
		}
		for line := 0; line < lines; line++ {
			idx := func(i int) int {
				i = clampInt(i, 0, n-1)
				if pass == 0 {
					return (line*w + i) * 4
				}
				return (i*w + line) * 4
			}
			for c := 0; c < 3; c++ {
				sum := 0
				for d := -r; d <= r; d++ {
					sum += int(pix[idx(d)+c])
				}
				for i := 0; i < n; i++ {
					tmp[idx(i)+c] = uint8(sum / (2*r + 1))
					sum += int(pix[idx(i+r+1)+c]) - int(pix[idx(i-r)+c])
				}
			}
			for i := 0; i < n; i++ {
				tmp[idx(i)+3] = 255
			}
		}
		copy(pix, tmp)
	}
}

// NormalizeIllumination divides src by its background estimate per channel,
// so paper maps to near-white everywhere while ink keeps its colour and its
// contrast against the paper.
func NormalizeIllumination(src image.Image, bg *image.RGBA) *image.RGBA {
	out := toRGBA(src)
	for i := 0; i+3 < len(out.Pix); i += 4 {
		for c := 0; c < 3; c++ {
			b := int(bg.Pix[i+c])
			if b < 24 {
				b = 24 // near-black backgrounds: don't amplify noise to white
			}
			v := int(out.Pix[i+c]) * 250 / b
			if v > 255 {
				v = 255
			}
			out.Pix[i+c] = uint8(v)
		}
	}
	return out
}

// lumaPercentile returns the p-quantile (0..1) of BT.601 luma over src.
func lumaPercentile(img *image.RGBA, p float64) uint8 {
	var hist [256]int
	for i := 0; i+3 < len(img.Pix); i += 4 {
		hist[luma(img.Pix[i], img.Pix[i+1], img.Pix[i+2])]++
	}
	target := int(p * float64(len(img.Pix)/4))
	acc := 0
	for v, n := range hist {
		acc += n
		if acc > target {
			return uint8(v)
		}
	}
	return 255
}

// luma is BT.601 luma in 0..255.
func luma(r, g, b uint8) uint8 {
	return uint8((299*int(r) + 587*int(g) + 114*int(b)) / 1000)
}

// clampInt clamps v to [lo, hi].
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// medianUint8 returns the median of vs (vs is reordered); 0 for empty input.
func medianUint8(vs []uint8) uint8 {
	if len(vs) == 0 {
		return 0
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i] < vs[j] })
	return vs[len(vs)/2]
}

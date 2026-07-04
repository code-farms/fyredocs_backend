package imaging

import (
	"image"
	"image/draw"
	"math"
)

// Grayscale converts to 8-bit grayscale using BT.601 luma weights.
func Grayscale(src image.Image) *image.Gray {
	b := src.Bounds()
	out := image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), src, b.Min, draw.Src) // stdlib Gray conversion is BT.601
	return out
}

// AdaptiveThreshold binarizes using an integral-image local mean: a pixel is
// white when its value exceeds (1-bias) × the mean of the surrounding
// window×window block. window <= 0 selects max(15, min(w,h)/8, odd). This is
// the classic "scanned document" look — dark text on white regardless of
// uneven lighting.
func AdaptiveThreshold(src image.Image, window int, bias float64) *image.Gray {
	gray := Grayscale(src)
	b := gray.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return gray
	}

	if window <= 0 {
		window = min(w, h) / 8
		if window < 15 {
			window = 15
		}
	}
	if window%2 == 0 {
		window++
	}
	half := window / 2

	// Integral image with one row/col of zero padding.
	integral := make([]uint64, (w+1)*(h+1))
	for y := 0; y < h; y++ {
		var rowSum uint64
		for x := 0; x < w; x++ {
			rowSum += uint64(gray.Pix[y*gray.Stride+x])
			integral[(y+1)*(w+1)+(x+1)] = integral[y*(w+1)+(x+1)] + rowSum
		}
	}

	out := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		y0, y1 := y-half, y+half
		if y0 < 0 {
			y0 = 0
		}
		if y1 > h-1 {
			y1 = h - 1
		}
		for x := 0; x < w; x++ {
			x0, x1 := x-half, x+half
			if x0 < 0 {
				x0 = 0
			}
			if x1 > w-1 {
				x1 = w - 1
			}
			count := uint64((x1 - x0 + 1) * (y1 - y0 + 1))
			sum := integral[(y1+1)*(w+1)+(x1+1)] -
				integral[(y0)*(w+1)+(x1+1)] -
				integral[(y1+1)*(w+1)+(x0)] +
				integral[(y0)*(w+1)+(x0)]
			mean := float64(sum) / float64(count)

			v := float64(gray.Pix[y*gray.Stride+x])
			if v > mean*(1-bias) {
				out.Pix[y*out.Stride+x] = 255
			} else {
				out.Pix[y*out.Stride+x] = 0
			}
		}
	}
	return out
}

// ColorBoost applies a mild contrast curve and saturation boost, LUT-based.
// contrast is a multiplier around the 128 midpoint (e.g. 1.15); saturation
// scales each channel's distance from the pixel's gray value (e.g. 1.25).
func ColorBoost(src image.Image, contrast, saturation float64) *image.RGBA {
	// Contrast LUT.
	var lut [256]uint8
	for i := 0; i < 256; i++ {
		v := (float64(i)-128)*contrast + 128
		lut[i] = uint8(math.Max(0, math.Min(255, math.Round(v))))
	}

	b := src.Bounds()
	rgba, ok := src.(*image.RGBA)
	if !ok {
		rgba = image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Src)
		b = rgba.Bounds()
	}

	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			i := rgba.PixOffset(x, y)
			r := float64(rgba.Pix[i])
			g := float64(rgba.Pix[i+1])
			bl := float64(rgba.Pix[i+2])

			// Saturation: push channels away from the pixel's luma.
			gray := 0.299*r + 0.587*g + 0.114*bl
			sat := func(c float64) uint8 {
				v := gray + saturation*(c-gray)
				return uint8(math.Max(0, math.Min(255, math.Round(v))))
			}

			oi := out.PixOffset(x-b.Min.X+out.Bounds().Min.X, y-b.Min.Y+out.Bounds().Min.Y)
			out.Pix[oi] = lut[sat(r)]
			out.Pix[oi+1] = lut[sat(g)]
			out.Pix[oi+2] = lut[sat(bl)]
			out.Pix[oi+3] = rgba.Pix[i+3]
		}
	}
	return out
}

// Rotate rotates by 0, 90, 180, or 270 degrees clockwise. Any other angle
// returns the source unchanged.
func Rotate(src image.Image, degrees int) image.Image {
	degrees = ((degrees % 360) + 360) % 360
	if degrees == 0 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()

	var out draw.Image
	switch degrees {
	case 90, 270:
		out = image.NewRGBA(image.Rect(0, 0, h, w))
	case 180:
		out = image.NewRGBA(image.Rect(0, 0, w, h))
	default:
		return src
	}

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := src.At(b.Min.X+x, b.Min.Y+y)
			switch degrees {
			case 90: // clockwise: (x, y) → (h-1-y, x)
				out.Set(h-1-y, x, c)
			case 180:
				out.Set(w-1-x, h-1-y, c)
			case 270:
				out.Set(y, w-1-x, c)
			}
		}
	}
	return out
}


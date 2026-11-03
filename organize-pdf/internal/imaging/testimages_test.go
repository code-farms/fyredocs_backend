package imaging

import (
	"image"
	"image/color"
	"math"
)

// page returns a w×h RGBA filled with c.
func page(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, 255
	}
	return img
}

// disc paints a filled circle.
func disc(img *image.RGBA, cx, cy, r float64, c color.RGBA) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if math.Hypot(float64(x)-cx, float64(y)-cy) <= r {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

// ellipse paints a filled axis-aligned ellipse.
func ellipse(img *image.RGBA, cx, cy, rx, ry float64, c color.RGBA) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dx, dy := (float64(x)-cx)/rx, (float64(y)-cy)/ry
			if dx*dx+dy*dy <= 1 {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

// rgbAt returns the RGB at (x, y).
func rgbAt(img *image.RGBA, x, y int) (int, int, int) {
	c := img.RGBAAt(x, y)
	return int(c.R), int(c.G), int(c.B)
}

func lumaAt(img *image.RGBA, x, y int) int {
	r, g, b := rgbAt(img, x, y)
	return int(luma(uint8(r), uint8(g), uint8(b)))
}

var (
	paperGrey = color.RGBA{230, 228, 222, 255}
	skin      = color.RGBA{200, 150, 120, 255}
	ink       = color.RGBA{25, 25, 30, 255}
)

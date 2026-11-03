package imaging

import (
	"image/color"
	"testing"
)

func TestRemoveShadowsFlattensPaperAndKeepsInk(t *testing.T) {
	t.Parallel()
	const w, h = 400, 300
	img := page(w, h, paperGrey)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			k := 1 - 0.55*float64(x)/float64(w-1)
			c := paperGrey
			if y%60 >= 28 && y%60 <= 31 && x > 20 && x < w-20 {
				c = ink
			}
			img.SetRGBA(x, y, color.RGBA{uint8(float64(c.R) * k), uint8(float64(c.G) * k), uint8(float64(c.B) * k), 255})
		}
	}
	if lumaAt(img, w-10, 10) > 140 {
		t.Fatal("fixture: the right edge should start in shadow")
	}
	out := RemoveShadows(img)
	left, right := lumaAt(out, 10, 10), lumaAt(out, w-10, 10)
	if left < 235 || right < 235 || abs(left-right) > 15 {
		t.Errorf("paper after normalization: left %d right %d, want both ≈white", left, right)
	}
	if v := lumaAt(out, w-40, 29); v > 100 {
		t.Errorf("ink in the shadowed half washed out to %d", v)
	}
	if v := lumaAt(out, 40, 29); v > 100 {
		t.Errorf("ink in the lit half washed out to %d", v)
	}
}

func TestCleanBackgroundWhitensPaperKeepsColouredInk(t *testing.T) {
	t.Parallel()
	img := page(200, 200, paperGrey)
	ellipse(img, 100, 100, 40, 6, color.RGBA{190, 20, 20, 255}) // red ink stroke
	out := CleanBackground(img)
	if r, g, b := rgbAt(out, 10, 10); r != 255 || g != 255 || b != 255 {
		t.Errorf("paper → %d,%d,%d, want pure white", r, g, b)
	}
	if r, g, _ := rgbAt(out, 100, 100); r < 150 || g > 80 {
		t.Errorf("red ink → r%d g%d, want still red", r, g)
	}
}

func TestEstimateBackgroundIgnoresDenseText(t *testing.T) {
	t.Parallel()
	img := page(300, 300, paperGrey)
	for y := 40; y < 260; y += 6 {
		for x := 40; x < 260; x++ {
			img.SetRGBA(x, y, ink)
			img.SetRGBA(x, y+1, ink)
		}
	}
	bg := EstimateBackground(img)
	if v := lumaAt(bg, 150, 150); v < 215 {
		t.Errorf("background under dense text = %d, want ≈paper (the max filter must erase ink)", v)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

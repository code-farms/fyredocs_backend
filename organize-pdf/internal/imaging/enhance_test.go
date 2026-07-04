package imaging

import (
	"image"
	"image/color"
	"testing"
)

func TestGrayscaleLuma(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 3, 1))
	src.Set(0, 0, color.RGBA{255, 255, 255, 255})
	src.Set(1, 0, color.RGBA{0, 0, 0, 255})
	src.Set(2, 0, color.RGBA{255, 0, 0, 255})

	g := Grayscale(src)
	if v := g.Pix[0]; v != 255 {
		t.Errorf("white → %d, want 255", v)
	}
	if v := g.Pix[1]; v != 0 {
		t.Errorf("black → %d, want 0", v)
	}
	// Pure red luma ≈ 0.299*255 ≈ 76 (stdlib uses 0.299/0.587/0.114).
	if v := int(g.Pix[2]); v < 70 || v > 85 {
		t.Errorf("red → %d, want ≈76", v)
	}
}

func TestAdaptiveThresholdBilevel(t *testing.T) {
	// Light paper with dark "text" strip; uneven background gradient.
	src := image.NewGray(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			v := uint8(180 + x/2) // gradient 180→211
			if y >= 30 && y <= 33 {
				v = 40 // dark text line
			}
			src.Pix[y*src.Stride+x] = v
		}
	}

	out := AdaptiveThreshold(src, 0, 0.15)
	for i, v := range out.Pix {
		if v != 0 && v != 255 {
			t.Fatalf("pixel %d = %d, output must be bilevel", i, v)
		}
	}
	// Text row should be black, paper rows white.
	if out.Pix[31*out.Stride+32] != 0 {
		t.Error("text pixel should be black")
	}
	if out.Pix[10*out.Stride+32] != 255 {
		t.Error("paper pixel should be white")
	}
}

func TestColorBoostBounds(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 1))
	src.Set(0, 0, color.RGBA{255, 255, 255, 255}) // must not overflow
	src.Set(1, 0, color.RGBA{128, 128, 128, 255}) // gray midpoint ≈ stable

	out := ColorBoost(src, 1.15, 1.25)
	r, g, b, _ := out.At(0, 0).RGBA()
	if uint8(r>>8) != 255 || uint8(g>>8) != 255 || uint8(b>>8) != 255 {
		t.Error("white should stay white")
	}
	r, _, _, _ = out.At(1, 0).RGBA()
	if v := int(uint8(r >> 8)); v < 120 || v > 136 {
		t.Errorf("gray midpoint drifted to %d", v)
	}
}

func TestRotateDimensions(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 30, 20))
	src.Set(0, 0, color.RGBA{255, 0, 0, 255}) // TL marker

	for _, deg := range []int{90, 270} {
		out := Rotate(src, deg)
		b := out.Bounds()
		if b.Dx() != 20 || b.Dy() != 30 {
			t.Errorf("rotate %d: got %dx%d, want 20x30", deg, b.Dx(), b.Dy())
		}
	}

	out := Rotate(src, 180)
	if b := out.Bounds(); b.Dx() != 30 || b.Dy() != 20 {
		t.Errorf("rotate 180: got %dx%d, want 30x20", b.Dx(), b.Dy())
	}
	// TL marker moves to BR on 180.
	r, _, _, _ := out.At(29, 19).RGBA()
	if uint8(r>>8) != 255 {
		t.Error("rotate 180 should move TL marker to BR")
	}

	// 90° clockwise: (0,0) → (h-1, 0) = (19, 0).
	out90 := Rotate(src, 90)
	r, _, _, _ = out90.At(19, 0).RGBA()
	if uint8(r>>8) != 255 {
		t.Error("rotate 90 should move TL marker to top-right")
	}

	if Rotate(src, 0) != image.Image(src) {
		t.Error("rotate 0 should return the source unchanged")
	}
}

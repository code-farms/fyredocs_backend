package imaging

import (
	"image"
	"math"
	"testing"
)

func TestDownscaleToShrinks(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 1000, 500))
	out, scale := DownscaleTo(src, 500)
	if out.Bounds().Dx() != 500 || out.Bounds().Dy() != 250 {
		t.Errorf("got %v, want 500x250", out.Bounds())
	}
	if math.Abs(scale-0.5) > 1e-9 {
		t.Errorf("scale = %f, want 0.5", scale)
	}
}

func TestDownscaleToNoOp(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 300, 200))
	out, scale := DownscaleTo(src, 500)
	if out.Bounds().Dx() != 300 || out.Bounds().Dy() != 200 {
		t.Errorf("no-op resize changed dimensions: %v", out.Bounds())
	}
	if scale != 1 {
		t.Errorf("scale = %f, want 1", scale)
	}
}

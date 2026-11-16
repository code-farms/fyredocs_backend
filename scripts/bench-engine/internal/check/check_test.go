package check

import (
	"errors"
	"image"
	"testing"

	"bench-engine/internal/diff"
)

func grayRect(w, h int) *image.Gray {
	g := image.NewGray(image.Rect(0, 0, w, h))
	for i := range g.Pix {
		g.Pix[i] = uint8(i * 7)
	}
	return g
}

func TestCropCommonToleratesRoundingOnly(t *testing.T) {
	a, b := grayRect(100, 120), grayRect(101, 120)
	ca, cb := cropCommon(a, b, ssimSlackPx)
	if ca.Bounds().Dx() != 100 || cb.Bounds().Dx() != 100 {
		t.Fatalf("1 px wider should crop to 100, got %v %v", ca.Bounds(), cb.Bounds())
	}
	if _, err := diff.SSIM(ca, cb); err != nil {
		t.Fatalf("cropped renders should compare: %v", err)
	}
	// A real size change (10 px) is left alone so SSIM reports the mismatch.
	ca, cb = cropCommon(a, grayRect(110, 120), ssimSlackPx)
	if _, err := diff.SSIM(ca, cb); !errors.Is(err, diff.ErrSizeMismatch) {
		t.Fatalf("10 px difference must stay a mismatch, got %v", err)
	}
}

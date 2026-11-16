package diff

import (
	"image"
	"image/color"
	"math"
	"testing"
)

// pattern draws a deterministic test image with structure (stripes and a box)
// so SSIM and pHash have something to compare.
func pattern(w, h, shift int) *image.Gray {
	img := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := uint8(255)
			if ((x+shift)/6)%2 == 0 {
				v = 30
			}
			if x+shift > w/3 && x+shift < w/2 && y > h/3 && y < h/2 {
				v = 120
			}
			img.SetGray(x, y, color.Gray{Y: v})
		}
	}
	return img
}

func TestSSIM(t *testing.T) {
	a := pattern(96, 64, 0)
	if s, err := SSIM(a, pattern(96, 64, 0)); err != nil || math.Abs(s-1) > 1e-9 {
		t.Errorf("identical: %v, %v; want 1.0", s, err)
	}
	if s, _ := SSIM(a, pattern(96, 64, 1)); s >= 0.99 {
		t.Errorf("1-px shift: %v; want < 0.99", s)
	}
	inv := image.NewGray(a.Bounds())
	for i, v := range a.Pix {
		inv.Pix[i] = 255 - v
	}
	if s, _ := SSIM(a, inv); s > 0 {
		t.Errorf("inverted: %v; want <= 0", s)
	}
	if _, err := SSIM(a, pattern(95, 64, 0)); err != ErrSizeMismatch {
		t.Errorf("size mismatch: err = %v", err)
	}
}

func TestPHash(t *testing.T) {
	a := pattern(200, 150, 0)
	if d := Hamming(PHash(a), PHash(pattern(200, 150, 0))); d != 0 {
		t.Errorf("identical: distance %d", d)
	}
	noisy := pattern(200, 150, 0)
	for i := 0; i < len(noisy.Pix); i += 97 {
		noisy.Pix[i] ^= 0x10
	}
	if d := Hamming(PHash(a), PHash(noisy)); d > 6 {
		t.Errorf("light noise: distance %d, want <= 6", d)
	}
	other := image.NewGray(a.Bounds())
	for y := 0; y < 150; y++ {
		for x := 0; x < 200; x++ {
			other.SetGray(x, y, color.Gray{Y: uint8((x * y) % 256)})
		}
	}
	if d := Hamming(PHash(a), PHash(other)); d < 10 {
		t.Errorf("different image: distance %d, want >= 10", d)
	}
}

func TestCER(t *testing.T) {
	cases := []struct {
		ref, cand string
		want      float64
	}{
		{"hello world", "hello  world\n", 0},
		{"abcd", "abxd", 0.25},
		{"abcd", "", 1},
		{"", "", 0},
		{"", "x", 1},
		{"नमस्ते", "नमस्ते", 0},
	}
	for _, tc := range cases {
		if got := CER(tc.ref, tc.cand); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("CER(%q,%q) = %v, want %v", tc.ref, tc.cand, got, tc.want)
		}
	}
}

func TestWordRecall(t *testing.T) {
	if r := WordRecall("a b c d", "d c b a"); r != 1 {
		t.Errorf("reordered: %v", r)
	}
	if r := WordRecall("a a b c", "a b"); r != 0.5 {
		t.Errorf("multiset: %v, want 0.5", r)
	}
	if r := WordRecall("", "anything"); r != 1 {
		t.Errorf("empty reference: %v", r)
	}
}

func TestPageSizes(t *testing.T) {
	a := PageSize{612, 792}
	if !SameSize(a, PageSize{612.4, 791.6}) {
		t.Error("within tolerance should match")
	}
	if SameSize(a, PageSize{613, 792}) {
		t.Error("1pt off should not match")
	}
	if !SameSize(Rotated(a), PageSize{792, 612}) {
		t.Error("rotated size")
	}
}

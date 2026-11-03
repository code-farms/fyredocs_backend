package imaging

import (
	"math"
	"testing"
)

func TestLabelComponents(t *testing.T) {
	t.Parallel()
	const w, h = 40, 30
	mask := make([]bool, w*h)
	set := func(x0, y0, x1, y1 int) {
		for y := y0; y <= y1; y++ {
			for x := x0; x <= x1; x++ {
				mask[y*w+x] = true
			}
		}
	}
	set(0, 5, 4, 9)     // touches the left border, 25 px
	set(20, 10, 29, 19) // interior, 100 px
	set(35, 25, 35, 25) // single pixel: below minArea
	labels, comps := LabelComponents(mask, w, h, 2)
	if len(comps) != 2 {
		t.Fatalf("got %d components, want 2", len(comps))
	}
	if !comps[0].TouchesBorder || comps[0].Area != 25 || comps[1].TouchesBorder || comps[1].Area != 100 {
		t.Errorf("components %+v", comps)
	}
	if labels[25*w+35] != 0 {
		t.Error("a sub-minArea region must be labelled background")
	}
	if c := comps[1]; c.CentroidX() != 24.5 || c.FillRatio() != 1 {
		t.Errorf("centroid %v fill %v", c.CentroidX(), c.FillRatio())
	}
}

func TestFillRatioSeparatesDiscsFromRings(t *testing.T) {
	t.Parallel()
	const n = 41
	discMask := make([]bool, n*n)
	ringMask := make([]bool, n*n)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			d := math.Hypot(float64(x-20), float64(y-20))
			discMask[y*n+x] = d <= 15
			ringMask[y*n+x] = d <= 15 && d >= 12
		}
	}
	_, dc := LabelComponents(discMask, n, n, 1)
	_, rc := LabelComponents(ringMask, n, n, 1)
	if fr := dc[0].FillRatio(); fr < 0.70 || fr > 0.82 { // digital discs sit a little under π/4
		t.Errorf("disc fill ratio %v, want ≈π/4", fr)
	}
	if fr := rc[0].FillRatio(); fr > 0.5 {
		t.Errorf("ring fill ratio %v, want low", fr)
	}
}

package imaging

import (
	"testing"
	"time"
)

// TestCleanupBudgetOnAPhonePhoto keeps the whole clean-up pipeline inside a
// per-page budget on a 12 MP capture — the scan worker processes up to 50
// pages per job. Skipped under -short.
func TestCleanupBudgetOnAPhonePhoto(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	img := page(4000, 3000, paperGrey)
	ellipse(img, 0, 1500, 300, 200, skin)
	start := time.Now()
	_, rep := Cleanup(img, CleanupOptions{Fingers: true, Holes: true, Glare: true, Background: true})
	took := time.Since(start)
	if rep.Fingers != 1 {
		t.Errorf("report %+v", rep)
	}
	if took > 8*time.Second {
		t.Errorf("12 MP clean-up took %v, budget 8s", took)
	}
	t.Logf("12 MP clean-up: %v", took)
}

func BenchmarkCoonsWarp12MP(b *testing.B) {
	img := page(4000, 3000, paperGrey)
	q := Quad{TL: Point{0, 0}, TR: Point{3999, 0}, BR: Point{3999, 2999}, BL: Point{0, 2999}}
	m := StraightMids(q)
	m.Top.Y += 80
	for i := 0; i < b.N; i++ {
		_, _ = CoonsWarp(img, q, m, 3000, 2250)
	}
}

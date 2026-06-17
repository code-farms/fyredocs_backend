package handlers

import (
	"math"
	"testing"
)

func TestRatio(t *testing.T) {
	cases := []struct {
		num, den, want float64
	}{
		{9, 10, 0.9},
		{0, 0, 0},
		{5, 0, 0},
		{1, 4, 0.25},
	}
	for _, c := range cases {
		if got := ratio(c.num, c.den); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("ratio(%v, %v) = %v, want %v", c.num, c.den, got, c.want)
		}
	}
}

func TestEstimateMRR(t *testing.T) {
	// PriceOf falls back to 0 for unconfigured plans; defaults include pro=12.
	counts := []planCountRow{{PlanName: "pro", Users: 10}, {PlanName: "free", Users: 100}}
	got := estimateMRR(counts)
	if got != 120 {
		t.Errorf("estimateMRR = %v, want 120 (10 pro × $12)", got)
	}
}

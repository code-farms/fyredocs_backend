package report

import (
	"fmt"
	"math"
	"sort"
)

// Thresholds are the comparison's regression limits.
type Thresholds struct {
	WallP50Pct float64 // slower p50 wall time per tool × category, in percent
	SSIMDrop   float64 // fall in a job's SSIM
}

// DefaultThresholds: +10 % wall p50, SSIM −0.02. The doc-16 noise target is
// ±5 %, so +10 % is twice the floor a stable host should show.
var DefaultThresholds = Thresholds{WallP50Pct: 10, SSIMDrop: 0.02}

// Change is one difference between two runs.
type Change struct {
	Kind   string // regression | improvement
	Where  string
	Detail string
}

// Compare lists regressions and improvements of cand against base. Both are
// reported; they are never netted against each other.
func Compare(base, cand *Report, t Thresholds) []Change {
	var out []Change
	bs := map[Key]Summary{}
	for _, s := range Summarize(base) {
		bs[s.Key] = s
	}
	for _, c := range Summarize(cand) {
		b, ok := bs[c.Key]
		if !ok || b.WallP50 == 0 || c.WallP50 == 0 {
			continue
		}
		d := (c.WallP50 - b.WallP50) / b.WallP50 * 100
		where := c.Tool + " / " + c.Category
		switch {
		case d > t.WallP50Pct:
			out = append(out, Change{"regression", where, fmt.Sprintf("wall p50 %+.1f%% (%.0f → %.0f ms)", d, b.WallP50, c.WallP50)})
		case d < -t.WallP50Pct:
			out = append(out, Change{"improvement", where, fmt.Sprintf("wall p50 %+.1f%% (%.0f → %.0f ms)", d, b.WallP50, c.WallP50)})
		}
	}
	type jk struct{ item, tool string }
	baseRows := map[jk]Row{}
	for _, r := range base.Rows {
		if r.Run == 1 {
			baseRows[jk{r.Item, r.Tool}] = r
		}
	}
	for _, r := range cand.Rows {
		if r.Run != 1 {
			continue
		}
		b, ok := baseRows[jk{r.Item, r.Tool}]
		if !ok {
			continue
		}
		where := r.Item + " " + r.Tool
		if b.Status == "completed" && r.Status != "completed" {
			out = append(out, Change{"regression", where, "now " + r.Status + ": " + r.Error})
		}
		if b.Status != "completed" && r.Status == "completed" {
			out = append(out, Change{"improvement", where, "now completes"})
		}
		bc := map[string]float64{}
		bp := map[string]bool{}
		for _, c := range b.Checks {
			if c.NA == "" {
				bc[c.Name], bp[c.Name] = c.Value, c.Pass
			}
		}
		for _, c := range r.Checks {
			if c.NA != "" {
				continue
			}
			was, had := bp[c.Name]
			if !had {
				continue
			}
			switch {
			case was && !c.Pass:
				out = append(out, Change{"regression", where, fmt.Sprintf("check %s now fails (%.4g)", c.Name, c.Value)})
			case !was && c.Pass:
				out = append(out, Change{"improvement", where, fmt.Sprintf("check %s now passes", c.Name)})
			}
			if c.Name == "ssim" && bc[c.Name]-c.Value > t.SSIMDrop {
				out = append(out, Change{"regression", where, fmt.Sprintf("ssim %.4f → %.4f", bc[c.Name], c.Value)})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind == "regression"
		}
		return out[i].Where < out[j].Where
	})
	return out
}

// Noise is the largest absolute wall-p50 difference, in percent, between two
// runs of the same code: the floor below which a change cannot be told apart.
func Noise(a, b *Report) (maxPct float64, where string) {
	as := map[Key]Summary{}
	for _, s := range Summarize(a) {
		as[s.Key] = s
	}
	for _, s := range Summarize(b) {
		x, ok := as[s.Key]
		if !ok || x.WallP50 == 0 {
			continue
		}
		if d := math.Abs(s.WallP50-x.WallP50) / x.WallP50 * 100; d > maxPct {
			maxPct, where = d, s.Tool+" / "+s.Category
		}
	}
	return maxPct, where
}

// HasRegression reports whether any change is a regression.
func HasRegression(cs []Change) bool {
	for _, c := range cs {
		if c.Kind == "regression" {
			return true
		}
	}
	return false
}

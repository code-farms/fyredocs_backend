package report

import (
	"path/filepath"
	"strings"
	"testing"

	"bench-engine/internal/check"
	"bench-engine/internal/metrics"
)

func rep(wall int64, status string, ssim float64, pass bool) *Report {
	return &Report{Runs: 1, Rows: []Row{
		{Item: "a-001", Category: "pdf", Tool: "compress-pdf", Run: 1, Status: status, WallMs: wall,
			Checks: []check.Result{{Name: "ssim", Value: ssim, Pass: pass}}},
		{Item: "a-002", Category: "pdf", Tool: "compress-pdf", Run: 1, Status: "completed", WallMs: wall},
	}}
}

func TestPercentile(t *testing.T) {
	v := []float64{5, 1, 4, 2, 3}
	if p := Percentile(v, 50); p != 3 {
		t.Errorf("p50 = %v", p)
	}
	if p := Percentile(v, 95); p != 5 {
		t.Errorf("p95 = %v", p)
	}
	if p := Percentile(nil, 50); p != 0 {
		t.Errorf("empty = %v", p)
	}
}

func TestCompareDetectsRegressions(t *testing.T) {
	base := rep(1000, "completed", 0.99, true)
	if cs := Compare(base, rep(1050, "completed", 0.99, true), DefaultThresholds); HasRegression(cs) {
		t.Errorf("+5%% is within noise: %v", cs)
	}
	if cs := Compare(base, rep(1200, "completed", 0.99, true), DefaultThresholds); !HasRegression(cs) {
		t.Error("+20% wall time not flagged")
	}
	if cs := Compare(base, rep(1000, "completed", 0.95, true), DefaultThresholds); !HasRegression(cs) {
		t.Error("SSIM drop of 0.04 not flagged")
	}
	if cs := Compare(base, rep(1000, "completed", 0.99, false), DefaultThresholds); !HasRegression(cs) {
		t.Error("check flipping to fail not flagged")
	}
	if cs := Compare(base, rep(1000, "failed", 0.99, true), DefaultThresholds); !HasRegression(cs) {
		t.Error("job newly failing not flagged")
	}
}

func TestCompareReportsMixedChangesSeparately(t *testing.T) {
	base := rep(1000, "completed", 0.99, true)
	cand := rep(700, "completed", 0.99, false) // faster but a check broke
	cs := Compare(base, cand, DefaultThresholds)
	var reg, imp bool
	for _, c := range cs {
		reg = reg || c.Kind == "regression"
		imp = imp || c.Kind == "improvement"
	}
	if !reg || !imp {
		t.Errorf("want both a regression and an improvement, got %v", cs)
	}
}

func TestNoise(t *testing.T) {
	if n, _ := Noise(rep(1000, "completed", 1, true), rep(1030, "completed", 1, true)); n < 2.9 || n > 3.1 {
		t.Errorf("noise = %v, want ~3%%", n)
	}
}

func TestWriteLoadRoundTrip(t *testing.T) {
	base := filepath.Join(t.TempDir(), "r")
	r := rep(1000, "completed", 0.99, true)
	r.Date = "2026-11-13"
	if err := Write(base, r); err != nil {
		t.Fatal(err)
	}
	got, err := Load(base + ".json")
	if err != nil || len(got.Rows) != 2 || got.Date != r.Date {
		t.Fatalf("round trip: %+v, %v", got, err)
	}
}

func TestSummarizeExpectedFailures(t *testing.T) {
	r := &Report{Rows: []Row{
		{Item: "m-1", Category: "malformed", Tool: "repair-pdf", Run: 1, Status: "failed", Expected: "fail-or-ok"},
		{Item: "m-2", Category: "malformed", Tool: "repair-pdf", Run: 1, Status: "error", Expected: "fail-or-ok"},
		{Item: "m-3", Category: "malformed", Tool: "repair-pdf", Run: 1, Status: "rejected", Expected: "fail-or-ok"},
	}}
	s := Summarize(r)[0]
	if s.Failed != 3 || s.Unexpected != 1 {
		t.Errorf("failed %d unexpected %d; want 3 and 1 (a rejection is a clean failure, a harness error never expected)", s.Failed, s.Unexpected)
	}
}

func TestMarkdownUnsampledPeakIsNA(t *testing.T) {
	r := &Report{Rows: []Row{
		{Item: "a", Category: "pdf", Tool: "split-pdf", Status: "completed", WallMs: 300},
		{Item: "a", Category: "pdf", Tool: "compress-pdf", Status: "completed", WallMs: 3000,
			Resources: metrics.Sample{Frames: 3, PeakMiB: 70}},
	}}
	md := Markdown(r)
	if !strings.Contains(md, "| split-pdf | pdf | 1 | 0 (0) | 300 / 300 | 0 / 0 | n/a |") {
		t.Errorf("unsampled row should print n/a:\n%s", md)
	}
	if !strings.Contains(md, "| 70 / 70 |") {
		t.Errorf("sampled row should print its peak:\n%s", md)
	}
}

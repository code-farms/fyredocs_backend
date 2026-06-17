package pricing

import "testing"

func TestParsePrices(t *testing.T) {
	got := parsePrices("free=0,pro=12,enterprise=99")
	want := map[string]float64{"free": 0, "pro": 12, "enterprise": 99}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("parsePrices[%q] = %v, want %v", k, got[k], v)
		}
	}
}

func TestParsePrices_SkipsMalformed(t *testing.T) {
	got := parsePrices("free=0, ,pro=oops,bad,enterprise=99")
	if _, ok := got["pro"]; ok {
		t.Error("expected malformed 'pro=oops' to be skipped")
	}
	if got["free"] != 0 || got["enterprise"] != 99 {
		t.Errorf("valid entries not parsed: %+v", got)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 valid entries, got %d: %+v", len(got), got)
	}
}

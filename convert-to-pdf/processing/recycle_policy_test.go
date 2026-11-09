package processing

import "testing"

func TestRecycleCause(t *testing.T) {
	rss := func(b int64) func() int64 { return func() int64 { return b } }
	const mb = int64(1) << 20
	cases := []struct {
		name     string
		uses     int
		maxUses  int
		rss      int64
		maxRSSMB int
		want     string
	}{
		{"below both limits", 199, 200, 599 * mb, 600, ""},
		{"exactly at the use limit", 200, 200, 0, 600, "max_conversions"},
		{"over the RSS limit", 5, 200, 600*mb + 1, 600, "max_rss"},
		{"exactly at the RSS limit keeps it", 5, 200, 600 * mb, 600, ""},
		{"use limit disabled", 10_000, 0, 0, 600, ""},
		{"RSS limit disabled", 1, 200, 10_000 * mb, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := recycleCause(tc.uses, tc.maxUses, rss(tc.rss), tc.maxRSSMB); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRecycleCauseSkipsRSSScan: the /proc scan is not done when RSS recycling
// is off or the use limit already fired.
func TestRecycleCauseSkipsRSSScan(t *testing.T) {
	called := false
	scan := func() int64 { called = true; return 0 }
	recycleCause(1, 200, scan, 0)
	recycleCause(200, 200, scan, 600)
	if called {
		t.Error("RSS was scanned although it could not change the outcome")
	}
}

func TestEnvLimit(t *testing.T) {
	cases := map[string]int{"": 200, "0": 0, "50": 50, "-3": 200, "x": 200}
	for v, want := range cases {
		t.Setenv("UNOSERVER_MAX_CONVERSIONS", v)
		if got := maxConversions(); got != want {
			t.Errorf("UNOSERVER_MAX_CONVERSIONS=%q: got %d, want %d", v, got, want)
		}
	}
}

func TestParseStatPgrp(t *testing.T) {
	cases := []struct {
		line string
		want int
		ok   bool
	}{
		{"1234 (soffice.bin) S 1200 1200 1200 0 -1", 1200, true},
		// comm may contain spaces and ')'; fields count from the last ')'.
		{"77 (weird ) name) R 5 4242 4242 0", 4242, true},
		{"garbage", 0, false},
		{"9 (x) S 1", 0, false},
	}
	for _, tc := range cases {
		got, ok := parseStatPgrp([]byte(tc.line))
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseStatPgrp(%q) = %d,%v want %d,%v", tc.line, got, ok, tc.want, tc.ok)
		}
	}
}

func TestParseStatmResident(t *testing.T) {
	if n, ok := parseStatmResident([]byte("51234 20480 3000 10 0 4000 0\n")); !ok || n != 20480 {
		t.Errorf("got %d,%v want 20480,true", n, ok)
	}
	if _, ok := parseStatmResident([]byte("1")); ok {
		t.Error("short statm must not parse")
	}
}

func TestPoolUseCounting(t *testing.T) {
	p := withTestPool(t, "2002")
	for i := 1; i <= 3; i++ {
		if got := p.recordUse("2002"); got != i {
			t.Fatalf("recordUse #%d = %d", i, got)
		}
	}
	p.resetUses("2002")
	if got := p.recordUse("2002"); got != 1 {
		t.Errorf("count after reset = %d, want 1", got)
	}
}

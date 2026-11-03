package extract

import "testing"

func TestNormDate(t *testing.T) {
	cases := map[any]string{
		"2026-03-12":           "2026-03-12",
		"12 March 2026":        "2026-03-12",
		"Mar 12, 2026":         "2026-03-12",
		"25/03/2026":           "2026-03-25", // day-first, unambiguous
		"03/25/2026":           "2026-03-25", // month-first, unambiguous
		"03/04/2026":           "",           // ambiguous: dropped, never guessed
		"31/02/2026":           "",           // not a real date
		"2026-13-01":           "",
		"soon":                 "",
		float64(2026):          "",
		"2026-03-12T10:00:00Z": "2026-03-12",
	}
	for in, want := range cases {
		if got := normDate(in); got != want {
			t.Errorf("normDate(%v)=%q, want %q", in, got, want)
		}
	}
}

func TestNormAmount(t *testing.T) {
	cases := []struct {
		in   any
		want float64
		ok   bool
	}{
		{"₹1,23,456.50", 123456.50, true},
		{"1.234,50", 1234.50, true},
		{"$12", 12, true},
		{"1,234", 1234, true},
		{float64(9.999), 10, true},
		{"free", 0, false},
		{nil, 0, false},
	}
	for _, c := range cases {
		got, ok := normAmount(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("normAmount(%v)=%v,%v want %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestNormCurrencyEmailsPhonesURL(t *testing.T) {
	for in, want := range map[string]string{"₹": "INR", "inr": "INR", "Rs.": "INR", "€": "EUR", "¥": "", "dollars": ""} {
		if got := normCurrency(in); got != want {
			t.Errorf("normCurrency(%q)=%q want %q", in, got, want)
		}
	}
	emails := normEmails([]any{"A@Example.com", "a@example.com", "bad@", "x@localhost", "<b@c.io>"})
	if len(emails) != 2 || emails[0] != "a@example.com" || emails[1] != "b@c.io" {
		t.Errorf("emails %v", emails)
	}
	phones := normPhones([]any{"+91 98765 43210", "(022) 2345-6789", "123", "+91-98765-43210"})
	if len(phones) != 2 || phones[0] != "+919876543210" || phones[1] != "02223456789" {
		t.Errorf("phones %v", phones)
	}
	for in, want := range map[string]string{"acme.com": "https://acme.com", "http://a.io/x": "http://a.io/x", "ftp://a.io": "", "localhost": "", "a b.com": ""} {
		if got := normURL(in); got != want {
			t.Errorf("normURL(%q)=%q want %q", in, got, want)
		}
	}
}

func TestSanitizeTitle(t *testing.T) {
	if got := sanitizeTitle(`"Invoice: ACME/March*2026"`); got != "Invoice ACME March 2026" {
		t.Errorf("got %q", got)
	}
}

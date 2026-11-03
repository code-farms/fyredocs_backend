package extract

import (
	"math"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// clip trims s and cuts it to n runes.
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// str coerces a JSON value to a trimmed string ("" for objects/arrays/null).
func str(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	}
	return ""
}

// list coerces a value to a slice: arrays pass through, a scalar becomes one item.
func list(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	if v == nil {
		return nil
	}
	return []any{v}
}

var textMonthLayouts = []string{"2 January 2006", "2 Jan 2006", "January 2, 2006", "Jan 2, 2006", "2-Jan-2006", "02-Jan-2006", "Jan 2 2006"}

var slashDate = regexp.MustCompile(`^(\d{1,2})[/.\-](\d{1,2})[/.\-](\d{4})$`)

// normDate returns YYYY-MM-DD or "". Slash/dot dates are converted only when
// unambiguous (one part > 12): "03/04/2026" could be March or April, and a
// confidently wrong date is worse than a missing one.
func normDate(v any) string {
	s := str(v)
	if s == "" {
		return ""
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.Format("2006-01-02")
	}
	if len(s) > 10 {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.Format("2006-01-02")
		}
	}
	for _, l := range textMonthLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.Format("2006-01-02")
		}
	}
	if m := slashDate.FindStringSubmatch(s); m != nil {
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		y, _ := strconv.Atoi(m[3])
		var day, month int
		switch {
		case a > 12 && b <= 12:
			day, month = a, b
		case b > 12 && a <= 12:
			day, month = b, a
		default:
			return ""
		}
		t := time.Date(y, time.Month(month), day, 0, 0, 0, 0, time.UTC)
		if t.Day() == day && int(t.Month()) == month {
			return t.Format("2006-01-02")
		}
	}
	return ""
}

var amountJunk = regexp.MustCompile(`[^\d.,\-]`)

// normAmount parses "₹1,23,456.50", "1.234,50", "$12" → a number rounded to
// cents. The LAST of ',' or '.' is the decimal separator when it is followed
// by 1–2 digits; otherwise both are grouping.
func normAmount(v any) (float64, bool) {
	if f, ok := v.(float64); ok {
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return math.Round(f*100) / 100, true
	}
	s := amountJunk.ReplaceAllString(str(v), "")
	if s == "" {
		return 0, false
	}
	dec := strings.LastIndexAny(s, ".,")
	if dec >= 0 && len(s)-dec-1 >= 1 && len(s)-dec-1 <= 2 {
		s = strings.NewReplacer(",", "", ".", "").Replace(s[:dec]) + "." + s[dec+1:]
	} else {
		s = strings.NewReplacer(",", "", ".", "").Replace(s)
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return math.Round(f*100) / 100, true
}

var currencySymbols = map[string]string{"₹": "INR", "RS": "INR", "RS.": "INR", "$": "USD", "US$": "USD", "€": "EUR", "£": "GBP", "AED": "AED"}

var iso4217 = regexp.MustCompile(`^[A-Z]{3}$`)

// normCurrency returns an ISO 4217-shaped code or "". "¥" is deliberately
// unmapped: it is both JPY and CNY.
func normCurrency(v any) string {
	s := strings.ToUpper(str(v))
	if c, ok := currencySymbols[s]; ok {
		return c
	}
	if iso4217.MatchString(s) {
		return s
	}
	return ""
}

// normEmails keeps syntactically valid, lowercased, de-duplicated addresses.
func normEmails(v any) []string {
	var out []string
	seen := map[string]bool{}
	for _, item := range list(v) {
		s := strings.ToLower(strings.Trim(str(item), "<>"))
		addr, err := mail.ParseAddress(s)
		if err != nil || addr.Address != s || !strings.Contains(s[strings.IndexByte(s, '@')+1:], ".") || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
		if len(out) == maxList {
			break
		}
	}
	return out
}

// normPhones keeps 7–15 digit numbers as "+<digits>" (international) or
// "<digits>" (national), de-duplicated. Formatting is the app's job.
func normPhones(v any) []string {
	var out []string
	seen := map[string]bool{}
	for _, item := range list(v) {
		s := str(item)
		plus := strings.HasPrefix(strings.TrimSpace(s), "+")
		var digits strings.Builder
		for _, r := range s {
			if r >= '0' && r <= '9' {
				digits.WriteRune(r)
			}
		}
		d := digits.String()
		if len(d) < 7 || len(d) > 15 {
			continue
		}
		if plus {
			d = "+" + d
		}
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
		if len(out) == maxList {
			break
		}
	}
	return out
}

// normURL returns an http(s) URL with a dotted host, or "".
func normURL(v any) string {
	s := strings.TrimSpace(str(v))
	if s == "" || strings.ContainsAny(s, " \t\n") {
		return ""
	}
	lower := strings.ToLower(s)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		if strings.Contains(lower, "://") {
			return ""
		}
		s = "https://" + s
	}
	host := strings.SplitN(strings.SplitN(s, "://", 2)[1], "/", 2)[0]
	if !strings.Contains(host, ".") {
		return ""
	}
	return clip(s, maxString)
}

// normEnum returns v when it is one of allowed (case-insensitive), else fallback.
func normEnum(v any, allowed []string, fallback string) string {
	s := strings.ToLower(strings.ReplaceAll(str(v), " ", "_"))
	for _, a := range allowed {
		if s == a {
			return a
		}
	}
	return fallback
}

// sanitizeTitle makes a model-suggested name safe as a document name:
// no path or filename-hostile characters, no wrapping quotes, ≤ maxTitle runes.
func sanitizeTitle(v any) string {
	s := strings.Trim(str(v), "\"'` ")
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) || r < 32 {
			return ' '
		}
		return r
	}, s)
	return clip(strings.Join(strings.Fields(s), " "), maxTitle)
}

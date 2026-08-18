// Fuzz targets for the .env parsing surface. These run in main() of every
// service before anything else, so a panic here is a fleet-wide boot crash —
// the invariant is graceful handling of arbitrary operator input, never
// correctness of any particular DSN.
package config

import (
	"strings"
	"testing"
)

func FuzzUnquoteValue(f *testing.F) {
	for _, seed := range []string{
		``, `x`, `"a"`, `'b'`, `""`, `''`, `"unterminated`, `'a"`,
		` "padded" `, `"nested 'quotes'"`, `"multi
line"`, `"`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := unquoteValue(s)
		if len(out) > len(s) {
			t.Errorf("unquoteValue(%q) grew the value: %q", s, out)
		}
		// Only a matching surrounding quote pair may be stripped; anything
		// else must pass through untouched (a stray quote inside a password
		// must never be eaten).
		quoted := len(s) >= 2 && s[0] == s[len(s)-1] && (s[0] == '"' || s[0] == '\'')
		if !quoted && out != s {
			t.Errorf("unquoteValue(%q) modified an unquoted value: %q", s, out)
		}
	})
}

func FuzzApplyPostgresDSNDefaults(f *testing.F) {
	for _, seed := range []string{
		"",
		"postgres://u:p@localhost:5432/db?sslmode=disable",
		"postgres://u:p@localhost/db?statement_timeout=1",
		"postgresql://u@h/db",
		"not a url at all",
		"postgres://u:p@localhost/db?a=1&a=2",
		"postgres://[::1]:5432/db",
		"postgres://u:p@localhost/db#frag",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, dsn string) {
		out := ApplyPostgresDSNDefaults(dsn)
		// Idempotence is the property callers depend on: operators often ship
		// DSNs that already carry the defaults, and a second application (env
		// reload, test re-run) must not mutate them further.
		if again := ApplyPostgresDSNDefaults(out); again != out {
			t.Errorf("not idempotent:\n first = %q\nsecond = %q", out, again)
		}
		if strings.TrimSpace(dsn) == "" && out != dsn {
			t.Errorf("empty DSN was rewritten to %q", out)
		}
	})
}

package config

import (
	"net/url"
	"strings"
)

// ApplyPostgresDSNDefaults returns dsn with safe server-side defaults appended
// as query parameters. Existing values in dsn are preserved. The defaults
// guarantee that:
//   - the server kills any query running longer than statement_timeout
//   - idle transactions are closed
//
// Both are standard Postgres GUCs that the server accepts as startup parameters,
// so they work against any Postgres (a co-located container, RDS, Neon, ...).
//
// NOTE: libpq TCP-keepalive parameters (keepalives, keepalives_idle, ...) are
// deliberately NOT added here. They are libpq-only client settings; the pgx
// driver this project uses does not consume them and instead forwards them to
// the server as runtime parameters, which a standard Postgres rejects with
// FATAL: unrecognized configuration parameter "keepalives_idle". (Neon's
// connection pooler silently ignored them, so they were never actually applied
// even there.) TCP keepalives, if needed for a remote DB, belong in the pgx
// pool/dialer config, not the DSN.
//
// The function is DSN-shape aware: it accepts both URI form
// (postgres://user:pass@host/db?sslmode=...) and key=value form
// (host=... dbname=... sslmode=...). For unrecognised shapes it returns dsn
// unchanged so the caller can still attempt to connect.
func ApplyPostgresDSNDefaults(dsn string) string {
	defaults := map[string]string{
		"statement_timeout":                   "15000",
		"idle_in_transaction_session_timeout": "30000",
	}

	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return dsn
		}
		q := u.Query()
		for k, v := range defaults {
			if q.Get(k) == "" {
				q.Set(k, v)
			}
		}
		u.RawQuery = q.Encode()
		return u.String()
	}

	if strings.Contains(dsn, "=") {
		fields := strings.Fields(dsn)
		seen := make(map[string]bool, len(fields))
		for _, f := range fields {
			if eq := strings.IndexByte(f, '='); eq > 0 {
				seen[f[:eq]] = true
			}
		}
		var b strings.Builder
		b.WriteString(dsn)
		for k, v := range defaults {
			if !seen[k] {
				b.WriteByte(' ')
				b.WriteString(k)
				b.WriteByte('=')
				b.WriteString(v)
			}
		}
		return b.String()
	}

	return dsn
}

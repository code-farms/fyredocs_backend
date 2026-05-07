package config

import (
	"net/url"
	"strings"
)

// ApplyPostgresDSNDefaults returns dsn with safe defaults for managed Postgres
// pools (Neon, RDS Proxy, pgbouncer) appended as query parameters. Existing
// values in dsn are preserved. The defaults guarantee that:
//   - the server kills any query running longer than statement_timeout
//   - idle transactions are closed
//   - the kernel detects dead TCP sockets via libpq keepalives instead of
//     blocking indefinitely on a half-closed connection
//
// Without these, a stale pool connection (closed by the server while idle in
// our pool) causes the next INSERT/UPDATE to hang for minutes before returning
// "unexpected EOF" — which manifests as 4-minute SERVER_ERROR responses.
//
// The function is DSN-shape aware: it accepts both URI form
// (postgres://user:pass@host/db?sslmode=...) and key=value form
// (host=... dbname=... sslmode=...). For unrecognised shapes it returns dsn
// unchanged so the caller can still attempt to connect.
func ApplyPostgresDSNDefaults(dsn string) string {
	defaults := map[string]string{
		"statement_timeout":                   "15000",
		"idle_in_transaction_session_timeout": "30000",
		"keepalives":                          "1",
		"keepalives_idle":                     "30",
		"keepalives_interval":                 "10",
		"keepalives_count":                    "3",
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

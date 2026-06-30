package config

import (
	"net/url"
	"strings"
	"testing"
)

func parseURIQuery(t *testing.T, dsn string) url.Values {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse %q: %v", dsn, err)
	}
	return u.Query()
}

func TestApplyPostgresDSNDefaults_URIInjectsAllDefaults(t *testing.T) {
	dsn := "postgresql://user:pw@host.example/db?sslmode=require"
	got := ApplyPostgresDSNDefaults(dsn)
	q := parseURIQuery(t, got)

	wants := map[string]string{
		"sslmode":                             "require",
		"statement_timeout":                   "15000",
		"idle_in_transaction_session_timeout": "30000",
	}
	// libpq keepalive params must NOT be injected — pgx forwards them to the
	// server, which rejects them as unrecognized configuration parameters.
	for _, k := range []string{"keepalives", "keepalives_idle", "keepalives_interval", "keepalives_count"} {
		if v := q.Get(k); v != "" {
			t.Errorf("param %q should not be injected, got %q", k, v)
		}
	}
	for k, v := range wants {
		if got := q.Get(k); got != v {
			t.Errorf("param %q: want %q, got %q (full DSN: %s)", k, v, got, dsn)
		}
	}
}

func TestApplyPostgresDSNDefaults_PreservesUserSetValues(t *testing.T) {
	// User sets a longer statement_timeout and an explicit keepalive. The helper
	// must not clobber existing values (it only fills in missing defaults), and
	// it must not strip params the user chose to set themselves.
	dsn := "postgresql://user:pw@host/db?sslmode=require&statement_timeout=60000&keepalives_idle=120"
	got := ApplyPostgresDSNDefaults(dsn)
	q := parseURIQuery(t, got)
	if v := q.Get("statement_timeout"); v != "60000" {
		t.Errorf("statement_timeout was overwritten: got %q, want 60000", v)
	}
	if v := q.Get("keepalives_idle"); v != "120" {
		t.Errorf("user-set keepalives_idle was dropped/overwritten: got %q, want 120", v)
	}
}

func TestApplyPostgresDSNDefaults_AcceptsBothURISchemes(t *testing.T) {
	for _, scheme := range []string{"postgres", "postgresql"} {
		dsn := scheme + "://u:p@h/db"
		got := ApplyPostgresDSNDefaults(dsn)
		if !strings.Contains(got, "statement_timeout=15000") {
			t.Errorf("scheme %s: defaults not applied, got %q", scheme, got)
		}
	}
}

func TestApplyPostgresDSNDefaults_KeyValueDSN(t *testing.T) {
	dsn := "host=localhost user=test dbname=app sslmode=disable"
	got := ApplyPostgresDSNDefaults(dsn)
	for _, want := range []string{
		"statement_timeout=15000",
		"idle_in_transaction_session_timeout=30000",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("kv DSN missing %q: got %q", want, got)
		}
	}
	if strings.Contains(got, "keepalives") {
		t.Errorf("kv DSN must not inject libpq keepalive params: got %q", got)
	}
	for _, preserve := range []string{"host=localhost", "user=test", "dbname=app", "sslmode=disable"} {
		if !strings.Contains(got, preserve) {
			t.Errorf("kv DSN dropped original %q: got %q", preserve, got)
		}
	}
}

func TestApplyPostgresDSNDefaults_KeyValuePreservesUserSet(t *testing.T) {
	dsn := "host=localhost statement_timeout=5000"
	got := ApplyPostgresDSNDefaults(dsn)
	// statement_timeout already set; helper must not append a second one.
	if c := strings.Count(got, "statement_timeout="); c != 1 {
		t.Errorf("expected 1 occurrence of statement_timeout, got %d in %q", c, got)
	}
	if !strings.Contains(got, "statement_timeout=5000") {
		t.Errorf("user value lost: got %q", got)
	}
}

func TestApplyPostgresDSNDefaults_UnrecognisedShapeReturnedUnchanged(t *testing.T) {
	cases := []string{
		"",
		"this is not a dsn",
		"mysql://nope",
	}
	for _, dsn := range cases {
		if got := ApplyPostgresDSNDefaults(dsn); got != dsn {
			t.Errorf("unrecognised dsn %q was modified to %q", dsn, got)
		}
	}
}

func TestApplyPostgresDSNDefaults_MalformedURIReturnedUnchanged(t *testing.T) {
	// url.Parse is permissive; force an actual error with a control character.
	dsn := "postgres://u:p@host/db?\x7f=bad"
	got := ApplyPostgresDSNDefaults(dsn)
	if got != dsn {
		t.Errorf("malformed dsn was modified: got %q want %q", got, dsn)
	}
}

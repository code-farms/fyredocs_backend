// Tests for the user-service membership client's wire contract, against a
// stub server (USER_SERVICE_URL is read per call, so t.Setenv is the seam).
//
// Subtest order inside TestMembership is load-bearing: the package-level
// circuit breaker trips at 5 CONSECUTIVE failures, so the two
// dependency-failure rows run last and stay at two — -shuffle only reorders
// top-level tests, never subtests.
package orgclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"fyredocs/shared/authverify"
)

func TestMembership(t *testing.T) {
	const orgID = "0198c5b6-0000-7000-8000-00000000aaaa"
	const userID = "0198c5b6-0000-7000-8000-00000000bbbb"

	t.Run("member: role decoded, internal contract honored", func(t *testing.T) {
		var gotPath, gotUser, gotToken string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotUser = r.Header.Get("X-User-ID")
			gotToken = r.Header.Get(authverify.InternalTokenHeader)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"role":"editor"}}`))
		}))
		t.Cleanup(srv.Close)
		t.Setenv("USER_SERVICE_URL", srv.URL)
		t.Setenv("INTERNAL_API_TOKEN", "test-secret")

		role, member, err := Membership(context.Background(), orgID, userID)
		if err != nil || !member || role != "editor" {
			t.Fatalf("Membership = (%q, %v, %v), want (editor, true, nil)", role, member, err)
		}
		// The /internal/v1 mirror, not the public /api/orgs route — this hop
		// has no JWT to forward, only the shared secret plus the user id.
		if gotPath != "/internal/v1/orgs/"+orgID {
			t.Errorf("path = %q, want /internal/v1/orgs/%s", gotPath, orgID)
		}
		if gotUser != userID {
			t.Errorf("X-User-ID = %q, want %q", gotUser, userID)
		}
		if gotToken != "test-secret" {
			t.Errorf("%s = %q, want the configured secret", authverify.InternalTokenHeader, gotToken)
		}
	})

	// 404 and 403 are definitive "not a member" answers, NOT dependency
	// failures: they return err == nil so callers deny access without
	// tripping the breaker (a burst of non-member checks must not blackhole
	// membership checks for actual members).
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden} {
		t.Run(http.StatusText(status)+" means non-member, not error", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			t.Cleanup(srv.Close)
			t.Setenv("USER_SERVICE_URL", srv.URL)
			role, member, err := Membership(context.Background(), orgID, userID)
			if err != nil || member || role != "" {
				t.Errorf("Membership = (%q, %v, %v), want (\"\", false, nil)", role, member, err)
			}
		})
	}

	t.Run("malformed 200 body is an error, not a grant", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("{not json"))
		}))
		t.Cleanup(srv.Close)
		t.Setenv("USER_SERVICE_URL", srv.URL)
		if _, member, err := Membership(context.Background(), orgID, userID); err == nil || member {
			t.Errorf("garbled body: member=%v err=%v, want (false, non-nil)", member, err)
		}
	})

	t.Run("5xx is a dependency failure", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		t.Cleanup(srv.Close)
		t.Setenv("USER_SERVICE_URL", srv.URL)
		if _, member, err := Membership(context.Background(), orgID, userID); err == nil || member {
			t.Errorf("503: member=%v err=%v, want (false, non-nil)", member, err)
		}
	})
}

// TestBaseURLDefault pins the in-mesh default so a docker-compose without
// USER_SERVICE_URL keeps resolving, and that a trailing slash is trimmed
// (a double slash in the path would 404 on some routers).
func TestBaseURLDefault(t *testing.T) {
	t.Setenv("USER_SERVICE_URL", "")
	if got := baseURL(); got != "http://user-service:8090" {
		t.Errorf("default baseURL = %q", got)
	}
	t.Setenv("USER_SERVICE_URL", "http://u:1/ ")
	if got := baseURL(); got != "http://u:1" {
		t.Errorf("trimmed baseURL = %q", got)
	}
}

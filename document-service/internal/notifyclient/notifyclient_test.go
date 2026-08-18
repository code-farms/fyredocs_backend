// Tests pinning notifyclient's fail-open posture: Notify is best-effort BY
// CONTRACT (it returns nothing), so a notification-service outage must never
// panic or otherwise fail a document operation — an export that generated
// correctly but whose "ready" ping was lost is complete; an export failed by
// its own ping would not be.
//
// Subtest order inside TestNotify is load-bearing: the package-level circuit
// breaker trips at 5 CONSECUTIVE failures, so the failure rows run last and
// stay at two — -shuffle only reorders top-level tests, never subtests.
package notifyclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNotify(t *testing.T) {
	t.Run("delivers the internal wire contract", func(t *testing.T) {
		var gotPath, gotToken, gotCT string
		var gotBody notifyReq
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.Method + " " + r.URL.Path
			gotToken = r.Header.Get("X-Internal-Token")
			gotCT = r.Header.Get("Content-Type")
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			w.WriteHeader(http.StatusCreated)
		}))
		t.Cleanup(srv.Close)
		t.Setenv("NOTIFICATION_SERVICE_URL", srv.URL)
		t.Setenv("INTERNAL_API_TOKEN", "test-secret")

		Notify(context.Background(), "user-1", "export.ready", "Export ready", "documents.csv", "/app/exports", "exp-1")

		if gotPath != "POST /internal/notifications" {
			t.Errorf("request = %q, want POST /internal/notifications", gotPath)
		}
		if gotToken != "test-secret" || gotCT != "application/json" {
			t.Errorf("headers = (token %q, content-type %q)", gotToken, gotCT)
		}
		// The body shape is coupled to notification-service by JSON alone
		// (no compile-time check) — this is the drift guard for that seam.
		want := notifyReq{UserID: "user-1", Type: "export.ready", Title: "Export ready",
			Body: "documents.csv", Link: "/app/exports", SourceID: "exp-1"}
		if gotBody != want {
			t.Errorf("body = %+v, want %+v", gotBody, want)
		}
	})

	t.Run("5xx swallowed (fail-open)", func(t *testing.T) {
		hit := false
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hit = true
			w.WriteHeader(http.StatusInternalServerError)
		}))
		t.Cleanup(srv.Close)
		t.Setenv("NOTIFICATION_SERVICE_URL", srv.URL)
		// The whole assertion is that this call RETURNS: no panic, no way to
		// observe a failure from the caller's side.
		Notify(context.Background(), "user-1", "export.ready", "t", "b", "l", "s")
		if !hit {
			t.Error("notification-service was never called")
		}
	})

	t.Run("unreachable service swallowed (fail-open)", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		srv.Close() // connection refused from here on
		t.Setenv("NOTIFICATION_SERVICE_URL", srv.URL)
		Notify(context.Background(), "user-1", "export.ready", "t", "b", "l", "s")
	})
}

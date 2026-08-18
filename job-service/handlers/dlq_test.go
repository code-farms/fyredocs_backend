// Tests for the DLQ redrive endpoint. The invariants under guard: only a
// super-admin role reaches the queue at all, the redrivable-service allowlist
// is exact-match (no case folding, no substring or injection latitude), and a
// missing JetStream connection answers 503 instead of panicking. Everything
// past the nil-JS guard needs a live JetStream and is integration-tier.
package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"fyredocs/shared/natsconn"
)

// withNilJetStream forces the nil-JetStream guard path, restoring the boot
// value afterwards so other tests see whatever main() would have injected.
func withNilJetStream(t *testing.T) {
	t.Helper()
	prev := natsconn.JS
	natsconn.JS = nil
	t.Cleanup(func() { natsconn.JS = prev })
}

func newDLQRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/jobs/dlq/redrive", RedriveDLQ)
	return r
}

func doRedrive(t *testing.T, role string) (*httptest.ResponseRecorder, apiEnvelope) {
	t.Helper()
	headers := map[string]string{}
	if role != "" {
		headers["X-User-Role"] = role
	}
	return doJSON(t, newDLQRouter(), http.MethodPost, "/api/jobs/dlq/redrive", nil, headers)
}

func TestRedriveDLQ_RequiresSuperAdmin(t *testing.T) {
	withNilJetStream(t)

	tests := []struct {
		name       string
		role       string
		wantStatus int
	}{
		// Anonymous and every lesser role stop at the 403 gate.
		{"no role header", "", http.StatusForbidden},
		{"user", "user", http.StatusForbidden},
		{"admin", "admin", http.StatusForbidden},
		{"guest", "guest", http.StatusForbidden},
		// Role matching is exact and case-sensitive — an upstream that starts
		// title-casing roles must fail closed here, not silently pass.
		{"uppercase variant rejected", "SUPER-ADMIN", http.StatusForbidden},
		{"title-case variant rejected", "Super-Admin", http.StatusForbidden},
		{"underscore variant rejected", "super_admin", http.StatusForbidden},
		{"role with payload rejected", "super-admin; drop", http.StatusForbidden},
		// The genuine role passes the gate and then hits the nil-JS guard —
		// 503 proves the request got past authorization.
		{"super-admin passes gate", "super-admin", http.StatusServiceUnavailable},
		{"surrounding whitespace is trimmed", "  super-admin  ", http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, env := doRedrive(t, tt.role)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantStatus == http.StatusForbidden {
				if env.Error == nil || env.Error.Code != "AUTH_FORBIDDEN" {
					t.Errorf("error = %+v, want code AUTH_FORBIDDEN", env.Error)
				}
			}
		})
	}
}

func TestRedriveDLQ_NilJetStream503(t *testing.T) {
	withNilJetStream(t)

	rec, env := doRedrive(t, "super-admin")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when JetStream is not connected (body %s)", rec.Code, rec.Body.String())
	}
	if env.Error == nil || env.Error.Code != "SERVICE_UNAVAILABLE" {
		t.Errorf("error = %+v, want code SERVICE_UNAVAILABLE", env.Error)
	}
}

// TestRedrivableServices pins the allowlist that decides which DLQ subjects
// may be re-dispatched. Membership is exact-match: event-consumer DLQs hold
// JobEvents (not dispatchable payloads), so any name outside the four worker
// services — including case variants and adversarial strings — must be false,
// leaving the message parked for its owner instead of republishing it.
func TestRedrivableServices(t *testing.T) {
	allowed := []string{"convert-to-pdf", "convert-from-pdf", "organize-pdf", "optimize-pdf"}
	for _, service := range allowed {
		if !redrivableServices[service] {
			t.Errorf("redrivableServices[%q] = false, want true (worker DLQ must be redrivable)", service)
		}
	}
	if len(redrivableServices) != len(allowed) {
		t.Errorf("allowlist has %d entries, want exactly %d — a new entry needs its own redrive-safety review", len(redrivableServices), len(allowed))
	}

	rejected := []string{
		// Event-consumer DLQs: their messages are JobEvents, not job payloads.
		"auth-service", "notification-service", "analytics-service", "document-service", "job-service",
		// Empty subject remainder (subject was exactly "jobs.dlq.").
		"",
		// Injection-shaped and decorated names.
		"convert-from-pdf; drop", "convert-to-pdf.>", "jobs.dlq.convert-to-pdf",
		// Case variants: subjects are byte-exact, so folding must not happen.
		"CONVERT-TO-PDF", "Convert-To-Pdf", "Organize-PDF",
		// Whitespace variants: subject tokens are never trimmed.
		" convert-to-pdf", "convert-to-pdf ",
	}
	for _, service := range rejected {
		if redrivableServices[service] {
			t.Errorf("redrivableServices[%q] = true, want false", service)
		}
	}
}

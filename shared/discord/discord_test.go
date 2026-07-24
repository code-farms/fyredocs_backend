package discord

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestIsResolved(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name   string
		endsAt time.Time
		want   bool
	}{
		{"zero endsAt is firing", time.Time{}, false},
		{"future endsAt is firing", now.Add(time.Minute), false},
		{"past endsAt is resolved", now.Add(-time.Minute), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := Alert{EndsAt: tc.endsAt}
			if got := a.IsResolved(now); got != tc.want {
				t.Errorf("IsResolved=%v want %v", got, tc.want)
			}
		})
	}
}

func TestFingerprintStableAndDistinct(t *testing.T) {
	a := Alert{Labels: map[string]string{"alertname": "X", "job": "auth", "severity": "warning"}}
	// Same labels in a different insertion order → same fingerprint.
	b := Alert{Labels: map[string]string{"severity": "warning", "job": "auth", "alertname": "X"}}
	if a.fingerprint() != b.fingerprint() {
		t.Error("same label set must produce the same fingerprint regardless of order")
	}
	c := Alert{Labels: map[string]string{"alertname": "X", "job": "billing", "severity": "warning"}}
	if a.fingerprint() == c.fingerprint() {
		t.Error("different labels must produce different fingerprints")
	}
}

func TestThrottle(t *testing.T) {
	th := newThrottle(1 * time.Hour)
	fp := "fp1"
	t0 := time.Unix(1_700_000_000, 0)

	if !th.shouldNotify(fp, true, t0) {
		t.Fatal("first firing should notify")
	}
	if th.shouldNotify(fp, true, t0.Add(5*time.Minute)) {
		t.Error("still firing within repeat window should NOT notify")
	}
	if !th.shouldNotify(fp, true, t0.Add(2*time.Hour)) {
		t.Error("still firing after repeat window should notify again")
	}
	// Transition to resolved → notify once.
	if !th.shouldNotify(fp, false, t0.Add(2*time.Hour+time.Minute)) {
		t.Error("firing→resolved transition should notify")
	}
	// Resolved again (already forgotten) → no notify.
	if th.shouldNotify(fp, false, t0.Add(3*time.Hour)) {
		t.Error("resolved for an unknown/forgotten alert should NOT notify")
	}
	// Fires again after being resolved → notify.
	if !th.shouldNotify(fp, true, t0.Add(4*time.Hour)) {
		t.Error("re-firing after resolve should notify")
	}
}

func TestHumanizeAlertName(t *testing.T) {
	cases := map[string]string{
		"ServiceDown":           "Service Down",
		"HighServerErrorRate":   "High Server Error Rate",
		"DBPoolNearExhaustion":  "DB Pool Near Exhaustion",
		"DLQBacklog":            "DLQ Backlog",
		"HighRequestLatencyP95": "High Request Latency P95",
		"":                      "Alert",
	}
	for in, want := range cases {
		if got := humanizeAlertName(in); got != want {
			t.Errorf("humanizeAlertName(%q) = %q, want %q", in, got, want)
		}
	}
}

func fieldValue(e Embed, name string) (string, bool) {
	for _, f := range e.Fields {
		if f.Name == name {
			return f.Value, true
		}
	}
	return "", false
}

func TestEmbedForExecutiveCard(t *testing.T) {
	c := &Client{
		iconURL:      "https://fyredocs.com/logo.png",
		dashboardURL: "https://fyredocs.com/grafana",
		environment:  "production",
	}
	a := Alert{
		Labels:      map[string]string{"alertname": "ServiceDown", "severity": "critical", "job": "auth-service", "instance": "auth-service:8086"},
		Annotations: map[string]string{"summary": "auth-service is down", "description": "cannot scrape for 1m"},
		StartsAt:    time.Unix(1_700_000_000, 0),
	}

	// Firing critical.
	e := c.embedFor(a, false)
	if e.Color != colorCritical {
		t.Errorf("firing critical color = %d, want %d", e.Color, colorCritical)
	}
	if e.Title != "🔴 Service Down" {
		t.Errorf("title = %q, want %q", e.Title, "🔴 Service Down")
	}
	if v, ok := fieldValue(e, "Since"); !ok || !strings.HasPrefix(v, "<t:") {
		t.Errorf("Since field = %q, want a Discord relative timestamp", v)
	}
	if e.Author == nil || e.Author.Name != "Fyredocs Monitoring" || e.Author.IconURL != "https://fyredocs.com/logo.png" {
		t.Errorf("author wrong: %+v", e.Author)
	}
	if e.Footer == nil || e.Footer.Text != "Fyredocs • production" {
		t.Errorf("footer wrong: %+v", e.Footer)
	}
	if e.URL != "https://fyredocs.com/grafana" {
		t.Errorf("title URL = %q, want dashboard link", e.URL)
	}
	if e.Timestamp == "" {
		t.Error("expected a timestamp")
	}
	if v, ok := fieldValue(e, "Status"); !ok || !strings.Contains(v, "Firing") {
		t.Errorf("Status field = %q", v)
	}
	if v, ok := fieldValue(e, "Severity"); !ok || v != "Critical" {
		t.Errorf("Severity field = %q, want Critical", v)
	}
	if v, ok := fieldValue(e, "Service"); !ok || !strings.Contains(v, "auth-service") {
		t.Errorf("Service field = %q", v)
	}
	if _, ok := fieldValue(e, "Details"); !ok {
		t.Error("expected a Details field from the description annotation")
	}

	// Resolved → green + Resolved status.
	r := c.embedFor(a, true)
	if r.Color != colorResolved {
		t.Errorf("resolved color = %d, want %d", r.Color, colorResolved)
	}
	if v, _ := fieldValue(r, "Status"); !strings.Contains(v, "Resolved") {
		t.Errorf("resolved Status = %q", v)
	}
	if _, ok := fieldValue(r, "Cleared"); !ok {
		t.Error("resolved embed should have a 'Cleared' relative-time field")
	}

	// Warning → orange.
	w := c.embedFor(Alert{Labels: map[string]string{"alertname": "Slow", "severity": "warning"}}, false)
	if w.Color != colorWarning {
		t.Errorf("warning color = %d, want %d", w.Color, colorWarning)
	}
}

func TestSendDisabledIsNoop(t *testing.T) {
	c := &Client{} // no webhook URL
	if c.Enabled() {
		t.Fatal("empty client should be disabled")
	}
	if err := c.Send(nil, Message{Content: "x"}); err != nil {
		t.Errorf("disabled Send should be a nil no-op, got %v", err)
	}
}

func TestAlertWebhookHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var received int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&received, 1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	t.Setenv("DISCORD_WEBHOOK_URL", srv.URL)
	t.Setenv("DISCORD_ALERT_REPEAT", "1h")
	c := NewFromEnv()
	if !c.Enabled() {
		t.Fatal("client should be enabled with a webhook URL")
	}

	r := gin.New()
	r.POST("/internal/alerts/api/v2/alerts", AlertWebhookHandler(c))

	body, _ := json.Marshal([]Alert{
		{Labels: map[string]string{"alertname": "ServiceDown", "severity": "critical", "job": "auth-service"},
			Annotations: map[string]string{"summary": "auth down"}},
	})

	do := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/internal/alerts/api/v2/alerts", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(rec, req)
		return rec
	}

	if rec := do(); rec.Code != http.StatusOK {
		t.Fatalf("first POST status=%d want 200", rec.Code)
	}
	// Second identical firing POST within the repeat window must be throttled.
	if rec := do(); rec.Code != http.StatusOK {
		t.Fatalf("second POST status=%d want 200", rec.Code)
	}
	if got := atomic.LoadInt32(&received); got != 1 {
		t.Errorf("Discord should have been called exactly once (throttled), got %d", got)
	}
}

func TestAlertWebhookHandlerDisabledStill200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DISCORD_WEBHOOK_URL", "")
	c := NewFromEnv()
	r := gin.New()
	r.POST("/x", AlertWebhookHandler(c))

	body, _ := json.Marshal([]Alert{{Labels: map[string]string{"alertname": "X"}}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("disabled receiver should still 200, got %d", rec.Code)
	}
}

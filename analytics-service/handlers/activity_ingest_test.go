package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"fyredocs/shared/authverify"
)

func ingestRouter(seed gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if seed != nil {
		r.Use(seed)
	}
	r.POST("/api/activity/events", IngestActivity)
	r.GET("/api/activity/me", GetMyActivity)
	return r
}

func postEvents(t *testing.T, r *gin.Engine, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/activity/events", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func validEvent(id string) map[string]any {
	return map[string]any{
		"clientEventId": id,
		"eventType":     "auth.login",
		"status":        "success",
		"platform":      "web",
		"occurredAt":    time.Now().UTC().Format(time.RFC3339),
	}
}

func TestIngestActivity_EmptyBatchRejected(t *testing.T) {
	r := ingestRouter(nil)
	if w := postEvents(t, r, map[string]any{"events": []any{}}); w.Code != http.StatusBadRequest {
		t.Fatalf("empty batch: code = %d, want 400", w.Code)
	}
}

func TestIngestActivity_OversizedBatchRejected(t *testing.T) {
	r := ingestRouter(nil)
	events := make([]any, maxBatchEvents+1)
	for i := range events {
		events[i] = validEvent(fmt.Sprintf("e-%d", i))
	}
	if w := postEvents(t, r, map[string]any{"events": events}); w.Code != http.StatusBadRequest {
		t.Fatalf("oversized batch: code = %d, want 400", w.Code)
	}
}

func TestIngestActivity_OversizedBodyRejected(t *testing.T) {
	r := ingestRouter(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/activity/events",
		strings.NewReader(`{"events":[{"clientEventId":"`+strings.Repeat("x", maxBodyBytes)+`"}]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("oversized body: code = %d, want 400", w.Code)
	}
}

// TestIngestActivity_PartialAccept_AllRejected exercises the per-event
// rejection path end to end: invalid events must be reported individually
// with a 200, never fail the batch, and must not touch the database (models.DB
// is nil here — reaching a DB call would panic the recovery-free test router).
func TestIngestActivity_PartialAccept_AllRejected(t *testing.T) {
	r := ingestRouter(nil)
	w := postEvents(t, r, map[string]any{"events": []any{
		map[string]any{"eventType": "auth.login", "platform": "web"},                                          // missing clientEventId
		map[string]any{"clientEventId": "e2", "eventType": "evil.hack", "platform": "web"},                    // unknown domain
		map[string]any{"clientEventId": "e3", "eventType": "auth.login", "platform": "server"},                // reserved platform
		map[string]any{"clientEventId": "e4", "eventType": "auth.login", "platform": "web", "status": "done"}, // bad status
	}})
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	var envelope struct {
		Data struct {
			Accepted     int64           `json:"accepted"`
			Deduplicated int64           `json:"deduplicated"`
			Rejected     []rejectedEvent `json:"rejected"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Accepted != 0 {
		t.Errorf("accepted = %d, want 0", envelope.Data.Accepted)
	}
	if len(envelope.Data.Rejected) != 4 {
		t.Fatalf("rejected = %d entries, want 4: %+v", len(envelope.Data.Rejected), envelope.Data.Rejected)
	}
	for i, rej := range envelope.Data.Rejected {
		if rej.Index != i {
			t.Errorf("rejected[%d].Index = %d, want %d", i, rej.Index, i)
		}
		if rej.Reason == "" {
			t.Errorf("rejected[%d] has empty reason", i)
		}
	}
}

func TestGetMyActivity_AnonymousUnauthorized(t *testing.T) {
	r := ingestRouter(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/activity/me", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: code = %d, want 401", w.Code)
	}
}

func TestGetMyActivity_GuestUnauthorized(t *testing.T) {
	seed := func(c *gin.Context) {
		authverify.SetGinAuth(c, authverify.AuthContext{UserID: "0198c5d0-0000-7000-8000-000000000001", IsGuest: true})
		c.Next()
	}
	r := ingestRouter(seed)
	req := httptest.NewRequest(http.MethodGet, "/api/activity/me", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("guest: code = %d, want 401", w.Code)
	}
}

func TestValidateEvent(t *testing.T) {
	base := func() activityEventInput {
		return activityEventInput{
			ClientEventID: "e1",
			EventType:     "job.started",
			Status:        "started",
			Platform:      "ios",
		}
	}

	t.Run("valid passes", func(t *testing.T) {
		in := base()
		if reason := validateEvent(&in); reason != "" {
			t.Fatalf("validateEvent = %q, want empty", reason)
		}
	})
	t.Run("empty status passes (defaults later)", func(t *testing.T) {
		in := base()
		in.Status = ""
		if reason := validateEvent(&in); reason != "" {
			t.Fatalf("validateEvent = %q, want empty", reason)
		}
	})
	t.Run("long field rejected", func(t *testing.T) {
		in := base()
		in.ToolID = strings.Repeat("x", maxFieldLen+1)
		if reason := validateEvent(&in); reason == "" {
			t.Fatal("expected rejection for oversized field")
		}
	})
	t.Run("oversized metadata rejected", func(t *testing.T) {
		in := base()
		in.Metadata = map[string]json.RawMessage{
			"blob": json.RawMessage(`"` + strings.Repeat("x", maxMetadataBytes) + `"`),
		}
		if reason := validateEvent(&in); reason == "" {
			t.Fatal("expected rejection for oversized metadata")
		}
	})
}

func TestSanitizeMetadata_StripsSensitiveKeys(t *testing.T) {
	out := sanitizeMetadata(map[string]json.RawMessage{
		"fileSize":   json.RawMessage(`123`),
		"password":   json.RawMessage(`"hunter2"`),
		"resetToken": json.RawMessage(`"abc"`),
	})
	if out == nil {
		t.Fatal("expected surviving metadata, got nil")
	}
	var clean map[string]json.RawMessage
	if err := json.Unmarshal(out, &clean); err != nil {
		t.Fatal(err)
	}
	if _, ok := clean["fileSize"]; !ok {
		t.Error("safe key fileSize was dropped")
	}
	if _, ok := clean["password"]; ok {
		t.Error("sensitive key password survived")
	}
	if _, ok := clean["resetToken"]; ok {
		t.Error("sensitive key resetToken survived")
	}
}

func TestSanitizeMetadata_AllSensitiveBecomesNil(t *testing.T) {
	out := sanitizeMetadata(map[string]json.RawMessage{
		"token": json.RawMessage(`"t"`),
	})
	if out != nil {
		t.Fatalf("expected nil metadata, got %s", out)
	}
}

func TestBoundedJSON(t *testing.T) {
	if got := boundedJSON(nil); got != nil {
		t.Error("nil input should stay nil")
	}
	if got := boundedJSON(json.RawMessage(`{"theme":"dark"}`)); got == nil {
		t.Error("small valid JSON should survive")
	}
	if got := boundedJSON(json.RawMessage(`not json`)); got != nil {
		t.Error("invalid JSON should be dropped")
	}
	big := json.RawMessage(`"` + strings.Repeat("x", maxValueBytes+1) + `"`)
	if got := boundedJSON(big); got != nil {
		t.Error("oversized value should be dropped")
	}
}

func TestParseTimeParam(t *testing.T) {
	if _, _, ok := parseTimeParam(""); ok {
		t.Error("empty string should not parse")
	}
	if _, _, ok := parseTimeParam("yesterday"); ok {
		t.Error("garbage should not parse")
	}
	if _, dateOnly, ok := parseTimeParam("2026-08-17T10:00:00Z"); !ok || dateOnly {
		t.Errorf("RFC3339: ok=%v dateOnly=%v, want true/false", ok, dateOnly)
	}
	if _, dateOnly, ok := parseTimeParam("2026-08-17"); !ok || !dateOnly {
		t.Errorf("bare date: ok=%v dateOnly=%v, want true/true", ok, dateOnly)
	}
}

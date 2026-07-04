package queue

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSubjectForAnalytics(t *testing.T) {
	tests := []struct {
		eventType string
		expected  string
	}{
		{"user.signup", "analytics.events.user.signup"},
		{"user.login", "analytics.events.user.login"},
		{"job.created", "analytics.events.job.created"},
		{"plan.limit_hit", "analytics.events.plan.limit_hit"},
	}
	for _, tt := range tests {
		result := SubjectForAnalytics(tt.eventType)
		if result != tt.expected {
			t.Errorf("SubjectForAnalytics(%q) = %q, want %q", tt.eventType, result, tt.expected)
		}
	}
}

func TestAnalyticsEvent_Fields(t *testing.T) {
	event := AnalyticsEvent{
		EventType: "user.signup",
		UserID:    "abc-123",
		IsGuest:   false,
		PlanName:  "free",
		Timestamp: time.Now().UTC(),
	}
	if event.EventType != "user.signup" {
		t.Errorf("unexpected EventType: %s", event.EventType)
	}
	if event.UserID != "abc-123" {
		t.Errorf("unexpected UserID: %s", event.UserID)
	}
	if event.IsGuest {
		t.Error("expected IsGuest to be false")
	}
}

// TestAnalyticsEvent_JobIDRoundTrip verifies the JobID field survives the NATS
// JSON round-trip under the `jobId` key. This id links job.created analytics
// rows to their job.completed job-event rows so the reliability handler can
// compute processing-duration percentiles.
func TestAnalyticsEvent_JobIDRoundTrip(t *testing.T) {
	in := AnalyticsEvent{
		EventType: "job.created",
		JobID:     "11111111-2222-3333-4444-555555555555",
		ToolType:  "merge-pdf",
		Timestamp: time.Now().UTC(),
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"jobId":"11111111-2222-3333-4444-555555555555"`) {
		t.Errorf("expected jobId in JSON, got %s", data)
	}
	var out AnalyticsEvent
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.JobID != in.JobID {
		t.Errorf("JobID round-trip = %q, want %q", out.JobID, in.JobID)
	}

	// Omitted when empty (omitempty) so non-job analytics events stay clean.
	bare, _ := json.Marshal(AnalyticsEvent{EventType: "user.signup"})
	if strings.Contains(string(bare), "jobId") {
		t.Errorf("empty JobID should be omitted, got %s", bare)
	}
}

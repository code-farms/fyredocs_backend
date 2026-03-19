package queue

import (
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

package queue

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// AnalyticsEvent represents a tracking event for business metrics.
type AnalyticsEvent struct {
	EventType string          `json:"eventType"`
	UserID    string          `json:"userId,omitempty"`
	IsGuest   bool            `json:"isGuest,omitempty"`
	ToolType  string          `json:"toolType,omitempty"`
	PlanName  string          `json:"planName,omitempty"`
	FileSize  int64           `json:"fileSize,omitempty"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
}

// PublishAnalyticsEvent marshals and publishes an AnalyticsEvent to the analytics NATS stream.
func PublishAnalyticsEvent(ctx context.Context, js jetstream.JetStream, event AnalyticsEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	subject := "analytics.events." + event.EventType
	_, err = js.Publish(ctx, subject, data)
	return err
}

// SubjectForAnalytics returns the NATS subject for an analytics event type.
func SubjectForAnalytics(eventType string) string {
	return "analytics.events." + eventType
}

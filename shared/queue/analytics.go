// Package queue defines the event payloads and publish/consume helpers exchanged
// over NATS JetStream: analytics tracking events and job pipeline events (dispatch
// and status updates).
package queue

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// AnalyticsEvent represents a tracking event for business metrics.
type AnalyticsEvent struct {
	EventType string          `json:"eventType"`
	JobID     string          `json:"jobId,omitempty"`
	UserID    string          `json:"userId,omitempty"`
	IsGuest   bool            `json:"isGuest,omitempty"`
	ToolType  string          `json:"toolType,omitempty"`
	PlanName  string          `json:"planName,omitempty"`
	FileSize  int64           `json:"fileSize,omitempty"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
}

// PublishAnalyticsEvent marshals and publishes an AnalyticsEvent to the
// analytics NATS stream, carrying the current W3C trace context in the
// message headers so the analytics subscriber continues the same trace.
func PublishAnalyticsEvent(ctx context.Context, js jetstream.JetStream, event AnalyticsEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	subject := "analytics.events." + event.EventType
	ctx, span := startProducerSpan(ctx, subject)
	defer span.End()
	_, err = js.PublishMsg(ctx, &nats.Msg{
		Subject: subject,
		Data:    data,
		Header:  InjectTraceHeader(ctx),
	})
	if err != nil {
		span.RecordError(err)
	}
	return err
}

// SubjectForAnalytics returns the NATS subject for an analytics event type.
func SubjectForAnalytics(eventType string) string {
	return "analytics.events." + eventType
}

package queue

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// JobEvent represents an event in the PDF processing pipeline.
// Used for both job dispatch (jobs.dispatch.*) and status updates (jobs.events.*).
type JobEvent struct {
	EventType     string          `json:"eventType"`
	JobID         string          `json:"jobId"`
	ToolType      string          `json:"toolType"`
	InputPaths    []string        `json:"inputPaths,omitempty"`
	OutputPath    string          `json:"outputPath,omitempty"`
	Options       json.RawMessage `json:"options,omitempty"`
	Progress      int             `json:"progress,omitempty"`
	FailureReason string          `json:"failureReason,omitempty"`
	FileSize      int64           `json:"fileSize,omitempty"`
	Attempts      int             `json:"attempts"`
	CorrelationID string          `json:"correlationId"`
	Timestamp     time.Time       `json:"timestamp"`
}

// PublishJobEvent marshals and publishes a JobEvent to the given NATS JetStream subject.
func PublishJobEvent(ctx context.Context, js jetstream.JetStream, subject string, event JobEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = js.Publish(ctx, subject, data)
	return err
}

// SubjectForDispatch returns the NATS subject for dispatching jobs to a service.
func SubjectForDispatch(serviceName string) string {
	return "jobs.dispatch." + serviceName
}

// SubjectForEvent returns the NATS subject for a job lifecycle event.
func SubjectForEvent(eventType string) string {
	return "jobs.events." + eventType
}

package queue

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"fyredocs/shared/metrics"
)

// JobEvent represents an event in the PDF processing pipeline.
// Used for both job dispatch (jobs.dispatch.*) and status updates (jobs.events.*).
type JobEvent struct {
	EventType     string          `json:"eventType"`
	JobID         string          `json:"jobId"`
	UserID        string          `json:"userId,omitempty"`
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
	// Every worker routes its terminal transitions through here, so this is the
	// single choke point where the jobs_processed_total / jobs_failed_total
	// counters (shared/metrics) are emitted — one per job outcome, keyed by tool.
	recordTerminalJobMetric(event)
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = js.Publish(ctx, subject, data)
	return err
}

// recordTerminalJobMetric increments the job outcome counters for terminal job
// events. Non-terminal events (JobQueued/JobProgress) and dispatch events are
// ignored. The `reason` label is a fixed low-cardinality value on purpose —
// free-form failure messages would explode Prometheus series cardinality, and
// the dashboards only aggregate by tool_type.
func recordTerminalJobMetric(event JobEvent) {
	switch event.EventType {
	case "JobCompleted":
		metrics.JobsProcessed.WithLabelValues(event.ToolType, "completed").Inc()
	case "JobFailed":
		metrics.JobsFailed.WithLabelValues(event.ToolType, "error").Inc()
	}
}

// SubjectForDispatch returns the NATS subject for dispatching jobs to a service.
func SubjectForDispatch(serviceName string) string {
	return "jobs.dispatch." + serviceName
}

// SubjectForEvent returns the NATS subject for a job lifecycle event.
func SubjectForEvent(eventType string) string {
	return "jobs.events." + eventType
}

// SubjectForJobEvent returns a NATS subject scoped to a specific job,
// e.g. "jobs.events.<jobID>.JobProgress". This allows SSE consumers to
// subscribe with a per-job filter like "jobs.events.<jobID>.>".
func SubjectForJobEvent(jobID, eventType string) string {
	return "jobs.events." + jobID + "." + eventType
}

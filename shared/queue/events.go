package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
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
	// QueuedAt is stamped by job-service at dispatch and echoed by workers on
	// terminal events so end-to-end job latency (queued → terminal, including
	// retries and DLQ dwell) can be observed as job_duration_seconds.
	QueuedAt time.Time `json:"queuedAt,omitempty"`
}

// PublishJobEvent marshals and publishes a JobEvent to the given NATS
// JetStream subject, carrying the current W3C trace context in the message
// headers so consumers continue the same trace.
func PublishJobEvent(ctx context.Context, js jetstream.JetStream, subject string, event JobEvent) error {
	// A nil handle must surface as an error, not a panic: callers pass
	// natsconn.JS directly, which is nil whenever NATS never connected, and a
	// panic here fires AFTER the caller committed its job row — the recovery
	// middleware's 500 leaves a stranded row the client retries into a
	// duplicate.
	if js == nil {
		return fmt.Errorf("queue: jetstream not initialized")
	}
	// Every worker routes its terminal transitions through here, so this is the
	// single choke point where the job outcome counters and the end-to-end
	// latency histogram (shared/metrics) are emitted — once per job outcome.
	recordTerminalJobMetric(event)
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
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

// PublishRaw publishes pre-marshaled bytes (DLQ hops, redrives) while
// preserving the original message headers and stamping the current trace
// context on top, so a dead-lettered message keeps its end-to-end trace.
func PublishRaw(ctx context.Context, js jetstream.JetStream, subject string, data []byte, base nats.Header) error {
	// Same nil posture as PublishJobEvent: DLQ hops pass natsconn.JS directly.
	if js == nil {
		return fmt.Errorf("queue: jetstream not initialized")
	}
	ctx, span := startProducerSpan(ctx, subject)
	defer span.End()
	_, err := js.PublishMsg(ctx, &nats.Msg{
		Subject: subject,
		Data:    data,
		Header:  injectInto(ctx, cloneHeader(base)),
	})
	if err != nil {
		span.RecordError(err)
	}
	return err
}

// cloneHeader copies a header so re-publishing never mutates the consumed
// message's header map.
func cloneHeader(h nats.Header) nats.Header {
	if h == nil {
		return nats.Header{}
	}
	out := make(nats.Header, len(h))
	for k, v := range h {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// recordTerminalJobMetric increments the job outcome counters — and, when the
// event carries QueuedAt, observes end-to-end latency — for terminal job
// events. Non-terminal events (JobQueued/JobProgress) and dispatch events are
// ignored. The `reason` label is a fixed low-cardinality value on purpose —
// free-form failure messages would explode Prometheus series cardinality, and
// the dashboards only aggregate by tool_type.
func recordTerminalJobMetric(event JobEvent) {
	var status string
	switch event.EventType {
	case "JobCompleted":
		metrics.JobsProcessed.WithLabelValues(event.ToolType, "completed").Inc()
		status = "completed"
	case "JobFailed":
		metrics.JobsFailed.WithLabelValues(event.ToolType, "error").Inc()
		status = "failed"
	default:
		return
	}
	if event.QueuedAt.IsZero() {
		return
	}
	end := event.Timestamp
	if end.IsZero() {
		end = time.Now().UTC()
	}
	if d := end.Sub(event.QueuedAt); d >= 0 {
		metrics.JobDuration.WithLabelValues(event.ToolType, status).Observe(d.Seconds())
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

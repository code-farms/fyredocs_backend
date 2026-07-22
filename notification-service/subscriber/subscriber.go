// Package subscriber turns job-completion events into in-app notifications.
package subscriber

import (
	"context"
	"encoding/json"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"fyredocs/shared/natsconn"
	"fyredocs/shared/queue"

	"notification-service/internal/models"
)

// maxDeliver bounds redelivery; eventBackoff paces retries so a failing
// dependency (e.g. Postgres down) is retried with backoff instead of a hot loop.
const maxDeliver = 5

var eventBackoff = []time.Duration{time.Second, 5 * time.Second, 15 * time.Second, 30 * time.Second}

// nakOrDLQ backs off a retry until maxDeliver is hit, then parks the message in
// the DLQ and Terminates it so it stops redelivering (no poison-message loop).
func nakOrDLQ(msg jetstream.Msg, service string, cause error) {
	if meta, err := msg.Metadata(); err == nil && meta.NumDelivered >= maxDeliver {
		if natsconn.JS != nil {
			_, _ = natsconn.JS.Publish(context.Background(), "jobs.dlq."+service, msg.Data())
		}
		slog.Error("event dropped to DLQ after max retries",
			"op", "subscriber.dlq", "service", service, "error", cause, "deliveries", meta.NumDelivered)
		_ = msg.Term()
		return
	}
	_ = msg.Nak()
}

// Subscribers holds the running NATS consumers so they can be stopped on
// shutdown.
type Subscribers struct {
	jobs jetstream.ConsumeContext
}

// Start subscribes to the job-events stream and begins turning completion and
// failure events into notifications. It uses a durable consumer so redeliveries
// survive restarts, and returns the running handle for graceful shutdown.
func Start(ctx context.Context) (*Subscribers, error) {
	consumer, err := natsconn.JS.CreateOrUpdateConsumer(ctx, "JOBS_EVENTS", jetstream.ConsumerConfig{
		Durable:       "notification-job-events",
		FilterSubject: "jobs.events.>",
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverNewPolicy,
		AckWait:       30 * time.Second,
		MaxDeliver:    maxDeliver,
		BackOff:       eventBackoff,
	})
	if err != nil {
		return nil, err
	}
	cc, err := consumer.Consume(handleJobEvent)
	if err != nil {
		return nil, err
	}
	slog.Info("notification subscriber started")
	return &Subscribers{jobs: cc}, nil
}

// Stop halts the consumers. It is safe to call on a nil or already-stopped
// Subscribers.
func (s *Subscribers) Stop() {
	if s == nil || s.jobs == nil {
		return
	}
	s.jobs.Stop()
	s.jobs = nil
}

func handleJobEvent(msg jetstream.Msg) {
	var event queue.JobEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		slog.Warn("notification: bad job event", "error", err)
		_ = msg.Ack()
		return
	}
	if strings.TrimSpace(event.UserID) == "" {
		_ = msg.Ack() // guests get no in-app feed
		return
	}
	uid, err := uuid.Parse(strings.TrimSpace(event.UserID))
	if err != nil {
		_ = msg.Ack()
		return
	}
	jobID, err := uuid.Parse(strings.TrimSpace(event.JobID))
	if err != nil {
		_ = msg.Ack()
		return
	}

	var notif models.Notification
	switch event.EventType {
	case "JobCompleted":
		name := path.Base(strings.TrimSpace(event.OutputPath))
		if name == "" || name == "." || name == "/" {
			name = event.ToolType + " output"
		}
		notif = models.Notification{
			UserID: uid, Type: "job.completed", SourceJobID: &jobID,
			Title: "Processing complete", Body: name, Link: "/app/documents",
		}
	case "JobFailed":
		reason := strings.TrimSpace(event.FailureReason)
		if reason == "" {
			reason = event.ToolType + " job failed"
		}
		notif = models.Notification{
			UserID: uid, Type: "job.failed", SourceJobID: &jobID,
			Title: "Processing failed", Body: reason, Link: "/app/documents",
		}
	default:
		_ = msg.Ack()
		return
	}

	// Idempotent on (user, source job).
	var count int64
	models.DB.Model(&models.Notification{}).Where("user_id = ? AND source_job_id = ?", uid, jobID).Count(&count)
	if count > 0 {
		_ = msg.Ack()
		return
	}
	if err := models.DB.Create(&notif).Error; err != nil {
		slog.Error("notification: create failed", "jobId", event.JobID, "error", err)
		nakOrDLQ(msg, "notification", err)
		return
	}

	// Live push: fan out to any connected SSE clients for this user via core
	// NATS (ephemeral; the durable copy already lives in Postgres).
	if natsconn.Conn != nil {
		if data, err := json.Marshal(notif); err == nil {
			_ = natsconn.Conn.Publish("notify."+uid.String(), data)
		}
	}
	_ = msg.Ack()
}

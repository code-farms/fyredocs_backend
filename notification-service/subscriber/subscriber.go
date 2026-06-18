// Package subscriber turns job-completion events into in-app notifications.
package subscriber

import (
	"context"
	"encoding/json"
	"log/slog"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"fyredocs/shared/natsconn"
	"fyredocs/shared/queue"

	"notification-service/internal/models"
)

type Subscribers struct {
	jobs jetstream.ConsumeContext
}

func Start(ctx context.Context) (*Subscribers, error) {
	consumer, err := natsconn.JS.CreateOrUpdateConsumer(ctx, "JOBS_EVENTS", jetstream.ConsumerConfig{
		Durable:       "notification-job-events",
		FilterSubject: "jobs.events.>",
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverNewPolicy,
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
		_ = msg.Nak()
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

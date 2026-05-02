package subscriber

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	"gorm.io/datatypes"

	"fyredocs/shared/natsconn"
	"fyredocs/shared/queue"

	"analytics-service/internal/models"
)

// Subscribers owns the JetStream consume contexts for the analytics-service.
// Stop must be called during graceful shutdown so the dispatcher goroutines
// finish before the NATS connection is drained.
type Subscribers struct {
	analytics jetstream.ConsumeContext
	jobs      jetstream.ConsumeContext
}

// Start subscribes to analytics and job event streams and persists events to the database.
// The returned Subscribers must have Stop called on shutdown.
func Start(ctx context.Context) (*Subscribers, error) {
	s := &Subscribers{}

	analyticsCtx, err := subscribeAnalyticsEvents(ctx)
	if err != nil {
		return nil, err
	}
	s.analytics = analyticsCtx

	jobsCtx, err := subscribeJobEvents(ctx)
	if err != nil {
		s.Stop()
		return nil, err
	}
	s.jobs = jobsCtx

	slog.Info("Analytics subscribers started")
	return s, nil
}

// Stop halts the dispatcher goroutines for both consumers. Safe to call on a
// nil receiver and idempotent.
func (s *Subscribers) Stop() {
	if s == nil {
		return
	}
	if s.analytics != nil {
		s.analytics.Stop()
		s.analytics = nil
	}
	if s.jobs != nil {
		s.jobs.Stop()
		s.jobs = nil
	}
}

func subscribeAnalyticsEvents(ctx context.Context) (jetstream.ConsumeContext, error) {
	consumer, err := natsconn.JS.CreateOrUpdateConsumer(ctx, "ANALYTICS", jetstream.ConsumerConfig{
		Durable:       "analytics-service",
		FilterSubject: "analytics.events.>",
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverNewPolicy,
	})
	if err != nil {
		return nil, err
	}

	return consumer.Consume(func(msg jetstream.Msg) {
		handleAnalyticsEvent(msg)
	})
}

func subscribeJobEvents(ctx context.Context) (jetstream.ConsumeContext, error) {
	consumer, err := natsconn.JS.CreateOrUpdateConsumer(ctx, "JOBS_EVENTS", jetstream.ConsumerConfig{
		Durable:       "analytics-job-events",
		FilterSubject: "jobs.events.>",
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverNewPolicy,
	})
	if err != nil {
		return nil, err
	}

	return consumer.Consume(func(msg jetstream.Msg) {
		handleJobEvent(msg)
	})
}

func handleAnalyticsEvent(msg jetstream.Msg) {
	var event queue.AnalyticsEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		slog.Warn("failed to unmarshal analytics event", "error", err)
		_ = msg.Ack()
		return
	}

	var userID *uuid.UUID
	if event.UserID != "" {
		parsed, err := uuid.Parse(event.UserID)
		if err == nil {
			userID = &parsed
		}
	}

	record := models.AnalyticsEvent{
		EventType:   event.EventType,
		UserID:      userID,
		IsGuest:     event.IsGuest,
		ToolType:    event.ToolType,
		PlanName:    event.PlanName,
		FileSize:    event.FileSize,
		Metadata:    datatypes.JSON(event.Metadata),
		CreatedAt:   event.Timestamp,
		PersistedAt: time.Now().UTC(),
	}

	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}

	if err := models.DB.Create(&record).Error; err != nil {
		slog.Error("failed to persist analytics event", "eventType", event.EventType, "error", err)
		_ = msg.Nak()
		return
	}

	_ = msg.Ack()
}

func handleJobEvent(msg jetstream.Msg) {
	var event queue.JobEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		slog.Warn("failed to unmarshal job event", "error", err)
		_ = msg.Ack()
		return
	}

	var eventType string
	switch event.EventType {
	case "JobCompleted":
		eventType = "job.completed"
	case "JobFailed":
		eventType = "job.failed"
	default:
		_ = msg.Ack()
		return
	}

	var userID *uuid.UUID
	if event.UserID != "" {
		parsed, err := uuid.Parse(event.UserID)
		if err == nil {
			userID = &parsed
		}
	}

	var jobID *uuid.UUID
	if event.JobID != "" {
		parsed, err := uuid.Parse(event.JobID)
		if err == nil {
			jobID = &parsed
		}
	}

	metaBytes, _ := json.Marshal(map[string]interface{}{
		"jobId":         event.JobID,
		"failureReason": event.FailureReason,
		"fileSize":      event.FileSize,
		"attempts":      event.Attempts,
	})

	record := models.AnalyticsEvent{
		EventType:   eventType,
		UserID:      userID,
		JobID:       jobID,
		ToolType:    event.ToolType,
		FileSize:    event.FileSize,
		Metadata:    datatypes.JSON(metaBytes),
		CreatedAt:   event.Timestamp,
		PersistedAt: time.Now().UTC(),
	}

	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}

	if err := models.DB.Create(&record).Error; err != nil {
		slog.Error("failed to persist job event", "eventType", eventType, "error", err)
		_ = msg.Nak()
		return
	}

	_ = msg.Ack()
}

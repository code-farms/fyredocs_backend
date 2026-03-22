package subscriber

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	"gorm.io/datatypes"

	"esydocs/shared/natsconn"
	"esydocs/shared/queue"

	"analytics-service/internal/models"
)

// Start subscribes to analytics and job event streams and persists events to the database.
func Start(ctx context.Context) error {
	if err := subscribeAnalyticsEvents(ctx); err != nil {
		return err
	}
	if err := subscribeJobEvents(ctx); err != nil {
		return err
	}
	slog.Info("Analytics subscribers started")
	return nil
}

func subscribeAnalyticsEvents(ctx context.Context) error {
	consumer, err := natsconn.JS.CreateOrUpdateConsumer(ctx, "ANALYTICS", jetstream.ConsumerConfig{
		Durable:       "analytics-service",
		FilterSubject: "analytics.events.>",
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverNewPolicy,
	})
	if err != nil {
		return err
	}

	_, err = consumer.Consume(func(msg jetstream.Msg) {
		handleAnalyticsEvent(msg)
	})
	return err
}

func subscribeJobEvents(ctx context.Context) error {
	consumer, err := natsconn.JS.CreateOrUpdateConsumer(ctx, "JOBS_EVENTS", jetstream.ConsumerConfig{
		Durable:       "analytics-job-events",
		FilterSubject: "jobs.events.>",
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverNewPolicy,
	})
	if err != nil {
		return err
	}

	_, err = consumer.Consume(func(msg jetstream.Msg) {
		handleJobEvent(msg)
	})
	return err
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

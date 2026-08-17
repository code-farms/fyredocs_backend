// Package subscriber consumes analytics events from NATS and persists them as
// AnalyticsEvent rows for later aggregation and querying.
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
	"fyredocs/shared/telemetry"

	"analytics-service/internal/activity"
	"analytics-service/internal/models"
)

// Subscribers owns the JetStream consume contexts for the analytics-service.
// Stop must be called during graceful shutdown so the dispatcher goroutines
// finish before the NATS connection is drained.
type Subscribers struct {
	analytics jetstream.ConsumeContext
	jobs      jetstream.ConsumeContext
}

// maxDeliver bounds redelivery; eventBackoff paces retries so a failing
// dependency (e.g. Postgres down) is retried with backoff instead of a hot loop.
const maxDeliver = 5

// eventBackoff is the redelivery schedule for event consumers. Analytics events
// are lower value than jobs, so the schedule trades promptness for load: a
// briefly unavailable database should not be hammered by retries.
var eventBackoff = []time.Duration{time.Second, 5 * time.Second, 15 * time.Second, 30 * time.Second}

// nakOrDLQ backs off a retry until maxDeliver is hit, then parks the message in
// the DLQ and Terminates it so it stops redelivering (no poison-message loop).
func nakOrDLQ(ctx context.Context, msg jetstream.Msg, service string, cause error) {
	if meta, err := msg.Metadata(); err == nil && meta.NumDelivered >= maxDeliver {
		if natsconn.JS != nil {
			// PublishRaw keeps the message's original headers (trace context)
			// so the dead-lettered copy stays in its end-to-end trace.
			_ = queue.PublishRaw(context.WithoutCancel(ctx), natsconn.JS, "jobs.dlq."+service, msg.Data(), msg.Headers())
		}
		slog.Error("event dropped to DLQ after max retries",
			"op", "subscriber.dlq", "service", service, "error", cause, "deliveries", meta.NumDelivered)
		_ = msg.Term()
		return
	}
	_ = msg.Nak()
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

// subscribeAnalyticsEvents consumes the analytics event stream.
func subscribeAnalyticsEvents(ctx context.Context) (jetstream.ConsumeContext, error) {
	consumer, err := natsconn.JS.CreateOrUpdateConsumer(ctx, "ANALYTICS", jetstream.ConsumerConfig{
		Durable:       "analytics-service",
		FilterSubject: "analytics.events.>",
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverNewPolicy,
		AckWait:       30 * time.Second,
		MaxDeliver:    maxDeliver,
		BackOff:       eventBackoff,
	})
	if err != nil {
		return nil, err
	}

	return consumer.Consume(func(msg jetstream.Msg) {
		handleAnalyticsEvent(context.Background(), msg)
	})
}

// subscribeJobEvents consumes job lifecycle events, kept a separate consumer from
// analytics events so a failure handling one stream cannot stall the other.
func subscribeJobEvents(ctx context.Context) (jetstream.ConsumeContext, error) {
	consumer, err := natsconn.JS.CreateOrUpdateConsumer(ctx, "JOBS_EVENTS", jetstream.ConsumerConfig{
		Durable:       "analytics-job-events",
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

	return consumer.Consume(func(msg jetstream.Msg) {
		handleJobEvent(context.Background(), msg)
	})
}

// handleAnalyticsEvent persists one analytics event.
//
// A malformed message is ACKed rather than retried: it will fail identically on
// every redelivery, so NAKing would spin until MaxDeliver with no chance of
// success. Only transient failures are worth redelivering.
func handleAnalyticsEvent(ctx context.Context, msg jetstream.Msg) {
	// Continue the publisher's trace (W3C context rides in the NATS headers).
	ctx, span := queue.StartConsumerSpan(ctx, telemetry.Tracer, msg.Subject(), msg.Headers())
	defer span.End()
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

	// JobID links job.created rows to their job.completed counterparts so the
	// reliability latency percentiles (processing duration) can be computed.
	var jobID *uuid.UUID
	if event.JobID != "" {
		parsed, err := uuid.Parse(event.JobID)
		if err == nil {
			jobID = &parsed
		}
	}

	record := models.AnalyticsEvent{
		EventType:   event.EventType,
		JobID:       jobID,
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

	if err := models.DB.WithContext(ctx).Create(&record).Error; err != nil {
		slog.Error("failed to persist analytics event", "eventType", event.EventType, "error", err)
		nakOrDLQ(ctx, msg, "analytics", err)
		return
	}

	recordAuditActivity(ctx, &record)

	_ = msg.Ack()
}

// auditedEventTypes maps analytics event names to their user_activity audit
// names for the server-origin subset: security and plan events the client
// cannot observe (and therefore cannot ingest itself). Client-observable
// events are deliberately absent — clients own those writes, and mapping them
// here too would double-count every login.
var auditedEventTypes = map[string]string{
	"user.refresh_reuse":            "auth.refresh_reuse",
	"user.password_reset_completed": "auth.password_reset",
	"user.proxy_login":              "auth.proxy_login",
	"plan.changed":                  "plan.changed",
	"plan.limit_hit":                "plan.limit_hit",
}

// recordAuditActivity mirrors a security/plan analytics event into the
// user_activity audit trail. Best-effort by design: the analytics row is
// already committed and ACK depends on it alone, so a failure here is logged
// and swallowed — NAKing would redeliver and duplicate the analytics row,
// which has no idempotency key.
func recordAuditActivity(ctx context.Context, record *models.AnalyticsEvent) {
	auditType, ok := auditedEventTypes[record.EventType]
	if !ok {
		return
	}

	row := models.UserActivity{
		UserID:     record.UserID,
		IsGuest:    record.IsGuest,
		EventType:  auditType,
		Status:     activity.StatusSuccess,
		Metadata:   record.Metadata,
		Platform:   "server",
		OccurredAt: record.CreatedAt,
	}
	if record.JobID != nil {
		row.CorrelationID = record.JobID.String()
	}

	if err := models.DB.WithContext(ctx).Create(&row).Error; err != nil {
		slog.Error("failed to persist audit activity", "eventType", auditType, "error", err)
	}
}

// handleJobEvent records a job lifecycle transition for reporting. Events are
// at-least-once, so writes here must tolerate seeing the same transition twice.
func handleJobEvent(ctx context.Context, msg jetstream.Msg) {
	// Continue the publisher's trace (W3C context rides in the NATS headers).
	ctx, span := queue.StartConsumerSpan(ctx, telemetry.Tracer, msg.Subject(), msg.Headers())
	defer span.End()
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

	if err := models.DB.WithContext(ctx).Create(&record).Error; err != nil {
		slog.Error("failed to persist job event", "eventType", eventType, "error", err)
		nakOrDLQ(ctx, msg, "analytics", err)
		return
	}

	_ = msg.Ack()
}

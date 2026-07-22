// Package subscriber consumes job-completion events and finalizes them into
// the document library. This is the server-side counterpart to direct document
// creation: every completed job for an authenticated user becomes a document,
// regardless of whether it came from the web app, the API, or elsewhere.
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
	"gorm.io/datatypes"

	"fyredocs/shared/natsconn"
	"fyredocs/shared/queue"

	"document-service/handlers"
	"document-service/internal/models"
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

// Subscribers owns the JetStream consume context for document finalize.
type Subscribers struct {
	jobs jetstream.ConsumeContext
}

// Start subscribes to job-completion events. Stop must be called on shutdown.
func Start(ctx context.Context) (*Subscribers, error) {
	consumer, err := natsconn.JS.CreateOrUpdateConsumer(ctx, "JOBS_EVENTS", jetstream.ConsumerConfig{
		Durable:       "document-job-events",
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
	slog.Info("document-service finalize subscriber started")
	return &Subscribers{jobs: cc}, nil
}

// Stop halts the consumer. Safe on a nil receiver and idempotent.
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
		slog.Warn("document finalize: bad job event", "error", err)
		_ = msg.Ack()
		return
	}

	// Only successful jobs for authenticated users become documents.
	if event.EventType != "JobCompleted" || strings.TrimSpace(event.UserID) == "" {
		_ = msg.Ack()
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

	// Idempotent: skip if a document already exists for this (user, job),
	// including soft-deleted ones (don't resurrect a doc the user trashed).
	var count int64
	models.DB.Unscoped().Model(&models.Document{}).
		Where("user_id = ? AND source_job_id = ?", uid, jobID).Count(&count)
	if count > 0 {
		_ = msg.Ack()
		return
	}

	// If the job was started in an org workspace, file the document there.
	orgID := handlers.WorkspaceForJob(context.Background(), models.DB, jobID, uid)

	name := documentName(event.OutputPath, event.ToolType)
	meta, _ := json.Marshal(map[string]any{"jobId": event.JobID, "toolType": event.ToolType})
	now := time.Now().UTC()
	doc := models.Document{
		UserID:         uid,
		OrganizationID: orgID,
		Name:           name,
		FileType:       fileExt(event.OutputPath),
		FileSize:       event.FileSize,
		StoragePath:    event.OutputPath,
		Status:         models.StatusReady,
		SourceJobID:    &jobID,
		Metadata:       datatypes.JSON(meta),
		ProcessedAt:    &now,
	}
	if err := models.DB.Create(&doc).Error; err != nil {
		slog.Error("document finalize: create failed", "jobId", event.JobID, "error", err)
		nakOrDLQ(msg, "document", err)
		return
	}
	if orgID != nil {
		handlers.ClearJobWorkspace(models.DB, jobID)
	}
	slog.Info("document finalized from job", "jobId", event.JobID, "documentId", doc.ID, "orgScoped", orgID != nil)
	_ = msg.Ack()
}

func documentName(outputPath, toolType string) string {
	base := path.Base(strings.TrimSpace(outputPath))
	if base != "" && base != "." && base != "/" {
		return base
	}
	if toolType != "" {
		return toolType + " output"
	}
	return "Processed document"
}

func fileExt(outputPath string) string {
	ext := strings.TrimPrefix(path.Ext(outputPath), ".")
	return strings.ToLower(ext)
}

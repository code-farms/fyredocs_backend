//go:build integration

// Integration tests for handleJobEvent's persistence half: the event-to-row
// mapping for terminal job events and the (user, source job) idempotency that
// makes JetStream's at-least-once delivery safe. The handler commits through
// models.DB and the dedupe count must see prior committed rows, so these
// tests use Truncate isolation, never dbtest.Tx (see testing-strategy.md).
// The pre-persistence filters (malformed events, guests, unmapped types) are
// unit-tested in subscriber_test.go, whose fakeMsg double is reused here.
package subscriber

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"fyredocs/shared/database/dbtest"
	"fyredocs/shared/natsconn"
	"fyredocs/shared/queue"

	"notification-service/internal/migrations"
	"notification-service/internal/models"
)

// withDB applies this service's migrations to the shared test database and
// swaps the package-global handle for the test's duration. The service name
// must stay "notification-service" — it selects the goose history table
// production writes (models.Migrate). natsconn.Conn is forced nil so the
// best-effort SSE fan-out stays inert. (A twin helper lives in the handlers
// package; promote to internal/testutil if a third package ever needs it.)
func withDB(t *testing.T) {
	t.Helper()
	db := dbtest.Open(t, "notification-service", migrations.FS)
	prev := models.DB
	models.DB = db
	t.Cleanup(func() { models.DB = prev })
	prevConn := natsconn.Conn
	natsconn.Conn = nil
	t.Cleanup(func() { natsconn.Conn = prevConn })
	dbtest.Truncate(t, db, "notifications")
}

// deliver runs handleJobEvent synchronously for one event and returns the
// message so the test can assert its disposition alongside the rows.
func deliver(t *testing.T, event queue.JobEvent) *fakeMsg {
	t.Helper()
	msg := &fakeMsg{data: mustEvent(t, event), subject: "jobs.events.test"}
	handleJobEvent(context.Background(), msg)
	return msg
}

// loadOnly fetches the single row for a user, failing if there are zero or
// several — the mapping tests assert on exactly one durable notification.
func loadOnly(t *testing.T, uid uuid.UUID) models.Notification {
	t.Helper()
	var rows []models.Notification
	if err := models.DB.Where("user_id = ?", uid).Find(&rows).Error; err != nil {
		t.Fatalf("load rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows for %s = %d, want exactly 1", uid, len(rows))
	}
	return rows[0]
}

// TestHandleJobEvent_CompletedMapsToNotificationRow pins the JobCompleted
// mapping the frontend feed renders: type job.completed, the fixed title, the
// output basename as body (with the tool-name fallback when the event carries
// no path), the documents link, and source_job_id carrying the job id.
func TestHandleJobEvent_CompletedMapsToNotificationRow(t *testing.T) {
	t.Run("with output path", func(t *testing.T) {
		withDB(t)
		uid := uuid.Must(uuid.NewV7())
		jid := uuid.Must(uuid.NewV7())
		msg := deliver(t, queue.JobEvent{
			EventType: "JobCompleted", JobID: jid.String(), UserID: uid.String(),
			ToolType: "merge-pdf", OutputPath: "results/2026/output.pdf",
		})
		if msg.acks != 1 || msg.naks != 0 || msg.terms != 0 {
			t.Errorf("disposition = %d acks / %d naks / %d terms, want exactly 1 ack", msg.acks, msg.naks, msg.terms)
		}
		row := loadOnly(t, uid)
		if row.Type != "job.completed" || row.Title != "Processing complete" ||
			row.Body != "output.pdf" || row.Link != "/app/documents" {
			t.Errorf("row = {type %q title %q body %q link %q}, want {job.completed, Processing complete, output.pdf, /app/documents}",
				row.Type, row.Title, row.Body, row.Link)
		}
		if row.SourceJobID == nil || *row.SourceJobID != jid {
			t.Errorf("row.SourceJobID = %v, want %s", row.SourceJobID, jid)
		}
		if row.ReadAt != nil {
			t.Error("new notification must start unread")
		}
	})

	t.Run("empty output path falls back to tool name", func(t *testing.T) {
		withDB(t)
		uid := uuid.Must(uuid.NewV7())
		msg := deliver(t, queue.JobEvent{
			EventType: "JobCompleted", JobID: uuid.Must(uuid.NewV7()).String(),
			UserID: uid.String(), ToolType: "split-pdf",
		})
		if msg.acks != 1 {
			t.Errorf("acks = %d, want 1", msg.acks)
		}
		if row := loadOnly(t, uid); row.Body != "split-pdf output" {
			t.Errorf("row.Body = %q, want %q", row.Body, "split-pdf output")
		}
	})
}

// TestHandleJobEvent_FailedMapsToNotificationRow pins the JobFailed mapping:
// type job.failed, the failure reason as body (with the tool-name fallback
// when the event carries none).
func TestHandleJobEvent_FailedMapsToNotificationRow(t *testing.T) {
	t.Run("with failure reason", func(t *testing.T) {
		withDB(t)
		uid := uuid.Must(uuid.NewV7())
		jid := uuid.Must(uuid.NewV7())
		msg := deliver(t, queue.JobEvent{
			EventType: "JobFailed", JobID: jid.String(), UserID: uid.String(),
			ToolType: "ocr-pdf", FailureReason: "file may be corrupted",
		})
		if msg.acks != 1 || msg.naks != 0 || msg.terms != 0 {
			t.Errorf("disposition = %d acks / %d naks / %d terms, want exactly 1 ack", msg.acks, msg.naks, msg.terms)
		}
		row := loadOnly(t, uid)
		if row.Type != "job.failed" || row.Title != "Processing failed" ||
			row.Body != "file may be corrupted" || row.Link != "/app/documents" {
			t.Errorf("row = {type %q title %q body %q link %q}, want {job.failed, Processing failed, file may be corrupted, /app/documents}",
				row.Type, row.Title, row.Body, row.Link)
		}
		if row.SourceJobID == nil || *row.SourceJobID != jid {
			t.Errorf("row.SourceJobID = %v, want %s", row.SourceJobID, jid)
		}
	})

	t.Run("empty reason falls back to tool name", func(t *testing.T) {
		withDB(t)
		uid := uuid.Must(uuid.NewV7())
		msg := deliver(t, queue.JobEvent{
			EventType: "JobFailed", JobID: uuid.Must(uuid.NewV7()).String(),
			UserID: uid.String(), ToolType: "ocr-pdf", FailureReason: "   ",
		})
		if msg.acks != 1 {
			t.Errorf("acks = %d, want 1", msg.acks)
		}
		if row := loadOnly(t, uid); row.Body != "ocr-pdf job failed" {
			t.Errorf("row.Body = %q, want %q", row.Body, "ocr-pdf job failed")
		}
	})
}

// TestHandleJobEvent_DuplicateDeliveryIsIdempotent pins the at-least-once
// contract: redelivering the same event (same user, same job) must ACK
// without inserting a second row — the dedupe count sees the committed first
// row, and the partial unique index idx_notif_user_source backstops the
// count-then-create race.
func TestHandleJobEvent_DuplicateDeliveryIsIdempotent(t *testing.T) {
	withDB(t)
	uid := uuid.Must(uuid.NewV7())
	event := queue.JobEvent{
		EventType: "JobCompleted", JobID: uuid.Must(uuid.NewV7()).String(),
		UserID: uid.String(), ToolType: "merge-pdf", OutputPath: "results/out.pdf",
	}

	first := deliver(t, event)
	redelivery := deliver(t, event)
	if first.acks != 1 || redelivery.acks != 1 {
		t.Errorf("acks = %d/%d, want 1/1 (a duplicate must still be acked, not retried)", first.acks, redelivery.acks)
	}
	if redelivery.naks != 0 || redelivery.terms != 0 {
		t.Errorf("redelivery naks/terms = %d/%d, want 0/0", redelivery.naks, redelivery.terms)
	}
	loadOnly(t, uid) // exactly one durable row survives the duplicate
}

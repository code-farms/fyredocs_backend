package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"fyredocs/shared/authverify"
	"fyredocs/shared/natsconn"
	"fyredocs/shared/queue"

	"job-service/internal/models"
)

// stubJob overrides the SSE ownership lookup so tests need no live database,
// restoring the original on cleanup.
func stubJob(t *testing.T, job *models.ProcessingJob, err error) {
	t.Helper()
	prev := loadJobByID
	loadJobByID = func(string) (*models.ProcessingJob, error) { return job, err }
	t.Cleanup(func() { loadJobByID = prev })
}

// ownerAuth returns a gin middleware that injects a verified auth context for uid,
// simulating what the gateway does after JWT verification.
func ownerAuth(uid uuid.UUID) gin.HandlerFunc {
	return func(c *gin.Context) {
		authverify.SetGinAuth(c, authverify.AuthContext{UserID: uid.String()})
		c.Next()
	}
}

func TestBuildSSEPayload(t *testing.T) {
	t.Run("failed event includes failureReason", func(t *testing.T) {
		p := buildSSEPayload(queue.JobEvent{
			JobID:         "job-1",
			EventType:     "JobFailed",
			Progress:      0,
			ToolType:      "ocr-pdf",
			FailureReason: "[CONVERSION_FAILED] failed to process page 1",
		})
		if p["status"] != "JobFailed" {
			t.Errorf("status = %v, want JobFailed", p["status"])
		}
		if p["failureReason"] != "[CONVERSION_FAILED] failed to process page 1" {
			t.Errorf("failureReason = %v, want the backend reason", p["failureReason"])
		}
	})

	t.Run("non-failed event omits failureReason and zero fileSize", func(t *testing.T) {
		p := buildSSEPayload(queue.JobEvent{
			JobID:     "job-2",
			EventType: "JobProgress",
			Progress:  40,
			ToolType:  "ocr-pdf",
		})
		if _, ok := p["failureReason"]; ok {
			t.Error("failureReason should be omitted when empty")
		}
		if _, ok := p["fileSize"]; ok {
			t.Error("fileSize should be omitted when zero")
		}
	})

	t.Run("completed event includes fileSize when set", func(t *testing.T) {
		p := buildSSEPayload(queue.JobEvent{
			JobID:     "job-3",
			EventType: "JobCompleted",
			Progress:  100,
			ToolType:  "ocr-pdf",
			FileSize:  12345,
		})
		if p["fileSize"] != int64(12345) {
			t.Errorf("fileSize = %v, want 12345", p["fileSize"])
		}
	})
}

// ownedJob returns a job owned by uid, plus a router that authenticates as uid,
// so the request passes the ownership gate and exercises the SSE path.
func ownedJobRouter(t *testing.T) *gin.Engine {
	t.Helper()
	uid := uuid.New()
	stubJob(t, &models.ProcessingJob{ID: uuid.New(), UserID: &uid}, nil)
	r := gin.New()
	r.Use(ownerAuth(uid))
	r.GET("/api/jobs/:id/events", SSEJobUpdates)
	return r
}

func TestSSEJobUpdates_NilJetStream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Ensure JetStream is nil
	originalJS := natsconn.JS
	natsconn.JS = nil
	defer func() { natsconn.JS = originalJS }()

	r := ownedJobRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/test-job-123/events", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	result := w.Result()
	defer result.Body.Close()

	// Should set SSE headers (auth passed, then JS nil)
	if ct := result.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected Content-Type text/event-stream, got %q", ct)
	}
	if cc := result.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("expected Cache-Control no-cache, got %q", cc)
	}
	if conn := result.Header.Get("Connection"); conn != "keep-alive" {
		t.Errorf("expected Connection keep-alive, got %q", conn)
	}
	if xab := result.Header.Get("X-Accel-Buffering"); xab != "no" {
		t.Errorf("expected X-Accel-Buffering no, got %q", xab)
	}

	body := w.Body.String()
	expected := "event: error\ndata: {\"message\":\"event stream unavailable\"}\n\n"
	if body != expected {
		t.Errorf("expected body %q, got %q", expected, body)
	}
}

func TestSSEJobUpdates_SetsSSEHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Ensure JetStream is nil so handler exits early after setting headers
	originalJS := natsconn.JS
	natsconn.JS = nil
	defer func() { natsconn.JS = originalJS }()

	r := ownedJobRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/abc-def-123/events", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	result := w.Result()
	defer result.Body.Close()

	headers := map[string]string{
		"Content-Type":      "text/event-stream",
		"Cache-Control":     "no-cache",
		"Connection":        "keep-alive",
		"X-Accel-Buffering": "no",
	}
	for key, want := range headers {
		got := result.Header.Get(key)
		if got != want {
			t.Errorf("header %s = %q, want %q", key, got, want)
		}
	}
}

// TestSSEJobUpdates_DeniesNonOwner verifies the ownership gate: an unauthenticated
// caller (or any non-owner) requesting another user's job gets 404 and NO SSE
// stream is opened.
func TestSSEJobUpdates_DeniesNonOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Job owned by some user; the request carries no auth context and no guest token.
	owner := uuid.New()
	stubJob(t, &models.ProcessingJob{ID: uuid.New(), UserID: &owner}, nil)

	r := gin.New() // no auth middleware → caller is anonymous
	r.GET("/api/jobs/:id/events", SSEJobUpdates)

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/someones-job/events", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a non-owner", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct == "text/event-stream" {
		t.Error("SSE stream must not be opened for an unauthorized caller")
	}
}

// TestSSEJobUpdates_JobNotFound verifies a missing job returns 404 without opening
// a stream.
func TestSSEJobUpdates_JobNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	stubJob(t, nil, errors.New("record not found"))

	r := gin.New()
	r.GET("/api/jobs/:id/events", SSEJobUpdates)

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/missing/events", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a missing job", w.Code)
	}
}

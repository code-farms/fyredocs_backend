package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

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

// ─── Client-disconnect resilience ─────────────────────────────────────────────

// sseFakeBatch is an empty jetstream.MessageBatch — a closed channel and no
// error, the shape of a Fetch that elapsed with nothing to deliver.
type sseFakeBatch struct{}

func (sseFakeBatch) Messages() <-chan jetstream.Msg {
	ch := make(chan jetstream.Msg)
	close(ch)
	return ch
}
func (sseFakeBatch) Error() error { return nil }

// sseFakeConsumer embeds the jetstream.Consumer interface so only the methods
// SSEJobUpdates actually calls need implementations; anything else panics on
// the nil embedded interface, which is the drift alarm we want if the handler
// starts using more of the consumer surface.
type sseFakeConsumer struct {
	jetstream.Consumer
	fetchStarted chan struct{} // closed on first Fetch: the stream is now mid-flight
	release      chan struct{} // Fetch blocks until closed (models FetchMaxWait)
	once         sync.Once
}

func (c *sseFakeConsumer) CachedInfo() *jetstream.ConsumerInfo {
	return &jetstream.ConsumerInfo{Name: "sse-fake"}
}

// Fetch blocks like a real pull consumer with no traffic, then returns an
// empty batch when released — the FetchMaxWait-expiry shape after which the
// stream loop re-checks its context.
func (c *sseFakeConsumer) Fetch(int, ...jetstream.FetchOpt) (jetstream.MessageBatch, error) {
	c.once.Do(func() { close(c.fetchStarted) })
	<-c.release
	return sseFakeBatch{}, nil
}

// sseFakeJS satisfies jetstream.JetStream via embedding, overriding just the
// consumer lifecycle calls the SSE handler makes.
type sseFakeJS struct {
	jetstream.JetStream
	cons *sseFakeConsumer
}

func (f *sseFakeJS) CreateConsumer(context.Context, string, jetstream.ConsumerConfig) (jetstream.Consumer, error) {
	return f.cons, nil
}
func (f *sseFakeJS) DeleteConsumer(context.Context, string, string) error { return nil }

// TestSSEJobUpdates_ClientDisconnectEndsHandler pins disconnect resilience: a
// client that goes away mid-stream (net/http surfaces a dropped connection as
// request-context cancellation) must end the handler promptly instead of
// leaving its goroutine pinned to a dead connection for the remainder of the
// 5-minute stream budget — under load, leaked SSE handlers accumulate per
// abandoned tab until the server runs out of them.
func TestSSEJobUpdates_ClientDisconnectEndsHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cons := &sseFakeConsumer{fetchStarted: make(chan struct{}), release: make(chan struct{})}
	originalJS := natsconn.JS
	natsconn.JS = &sseFakeJS{cons: cons}
	defer func() { natsconn.JS = originalJS }()

	r := ownedJobRouter(t)

	before := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/jobs/job-1/events", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		r.ServeHTTP(rec, req)
	}()

	// Wait until the handler is mid-stream (blocked in Fetch), then disconnect.
	select {
	case <-cons.fetchStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never reached Fetch — the stream did not start")
	}
	cancel()
	close(cons.release) // the in-flight Fetch returns, as it would at FetchMaxWait

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler leaked: still streaming 5s after the client disconnected")
	}

	// Safe to read the recorder once the handler goroutine has exited.
	if body := rec.Body.String(); !strings.Contains(body, "event: connected") {
		t.Errorf("stream never sent the connected event; body = %q", body)
	}

	// Belt and braces: total goroutines settle back to (roughly) the baseline.
	// Tolerance absorbs unrelated runtime/test-framework goroutines; the hard
	// assertion above (handler returned) is the real leak check.
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before+2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if g := runtime.NumGoroutine(); g > before+2 {
		t.Errorf("goroutines = %d, baseline %d (+2 tolerance) — SSE disconnect leaked goroutines", g, before)
	}
}

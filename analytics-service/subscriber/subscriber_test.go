package subscriber

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"analytics-service/internal/models"
)

type fakeConsumeContext struct {
	stopped atomic.Int32
	closed  chan struct{}
}

func newFakeConsumeContext() *fakeConsumeContext {
	return &fakeConsumeContext{closed: make(chan struct{})}
}

func (f *fakeConsumeContext) Stop() {
	f.stopped.Add(1)
	select {
	case <-f.closed:
	default:
		close(f.closed)
	}
}
func (f *fakeConsumeContext) Drain()                  { f.Stop() }
func (f *fakeConsumeContext) Closed() <-chan struct{} { return f.closed }

var _ jetstream.ConsumeContext = (*fakeConsumeContext)(nil)

func TestSubscribers_Stop_NilReceiver(t *testing.T) {
	var s *Subscribers
	s.Stop() // must not panic
}

func TestSubscribers_Stop_StopsBothConsumers(t *testing.T) {
	a := newFakeConsumeContext()
	j := newFakeConsumeContext()
	s := &Subscribers{analytics: a, jobs: j}

	s.Stop()

	if a.stopped.Load() != 1 {
		t.Errorf("analytics consumer Stop count = %d, want 1", a.stopped.Load())
	}
	if j.stopped.Load() != 1 {
		t.Errorf("jobs consumer Stop count = %d, want 1", j.stopped.Load())
	}
}

func TestSubscribers_Stop_Idempotent(t *testing.T) {
	a := newFakeConsumeContext()
	j := newFakeConsumeContext()
	s := &Subscribers{analytics: a, jobs: j}

	s.Stop()
	s.Stop() // second call must be a no-op, not double-Stop

	if a.stopped.Load() != 1 {
		t.Errorf("analytics consumer Stop called %d times, want 1", a.stopped.Load())
	}
	if j.stopped.Load() != 1 {
		t.Errorf("jobs consumer Stop called %d times, want 1", j.stopped.Load())
	}
}

func TestSubscribers_Stop_ClearsHandles(t *testing.T) {
	s := &Subscribers{analytics: newFakeConsumeContext(), jobs: newFakeConsumeContext()}
	s.Stop()

	if s.analytics != nil || s.jobs != nil {
		t.Error("Stop should clear consumer handles to make subsequent calls a no-op")
	}
}

// TestAuditedEventTypes_ServerOriginOnly guards the double-count boundary:
// only events the client cannot observe may be mirrored into user_activity.
// If a client-observable event (login, signup, job lifecycle) appears here,
// every occurrence will be recorded twice — once by the client SDK's ingest
// and once by this subscriber.
func TestAuditedEventTypes_ServerOriginOnly(t *testing.T) {
	clientObservable := []string{"user.login", "user.signup", "job.created", "job.completed", "job.failed"}
	for _, evt := range clientObservable {
		if _, ok := auditedEventTypes[evt]; ok {
			t.Errorf("client-observable event %q must not be subscriber-mapped (double count)", evt)
		}
	}

	expected := map[string]string{
		"user.refresh_reuse":            "auth.refresh_reuse",
		"user.password_reset_completed": "auth.password_reset",
		"user.proxy_login":              "auth.proxy_login",
		"plan.changed":                  "plan.changed",
		"plan.limit_hit":                "plan.limit_hit",
	}
	for from, want := range expected {
		if got := auditedEventTypes[from]; got != want {
			t.Errorf("auditedEventTypes[%q] = %q, want %q", from, got, want)
		}
	}
}

// TestRecordAuditActivity_UnmappedTypeIsNoop proves the mapping check happens
// before any database access: models.DB is nil in tests, so reaching the
// Create call would panic.
func TestRecordAuditActivity_UnmappedTypeIsNoop(t *testing.T) {
	recordAuditActivity(context.Background(), &models.AnalyticsEvent{EventType: "user.login"})
}

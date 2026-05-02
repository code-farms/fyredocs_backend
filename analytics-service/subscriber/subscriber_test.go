package subscriber

import (
	"sync/atomic"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
)

type fakeConsumeContext struct {
	stopped atomic.Int32
	closed  chan struct{}
}

func newFakeConsumeContext() *fakeConsumeContext {
	return &fakeConsumeContext{closed: make(chan struct{})}
}

func (f *fakeConsumeContext) Stop()                   { f.stopped.Add(1); select { case <-f.closed: default: close(f.closed) } }
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

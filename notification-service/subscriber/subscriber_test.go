package subscriber

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"fyredocs/shared/natsconn"
	"fyredocs/shared/queue"
)

// fakeMsg is a hand-rolled jetstream.Msg double (the repo's no-gomock
// convention) that records the terminal disposition a handler gave the
// message. handleJobEvent runs synchronously, so plain counters suffice.
type fakeMsg struct {
	data    []byte
	subject string
	// meta/metaErr feed Metadata(); nakOrDLQ treats a Metadata error as
	// "delivery count unknown" and retries.
	meta    *jetstream.MsgMetadata
	metaErr error

	acks, naks, terms int
}

func (m *fakeMsg) Metadata() (*jetstream.MsgMetadata, error) { return m.meta, m.metaErr }
func (m *fakeMsg) Data() []byte                              { return m.data }
func (m *fakeMsg) Headers() nats.Header                      { return nil }
func (m *fakeMsg) Subject() string                           { return m.subject }
func (m *fakeMsg) Reply() string                             { return "" }
func (m *fakeMsg) Ack() error                                { m.acks++; return nil }
func (m *fakeMsg) DoubleAck(context.Context) error           { m.acks++; return nil }
func (m *fakeMsg) Nak() error                                { m.naks++; return nil }
func (m *fakeMsg) NakWithDelay(time.Duration) error          { m.naks++; return nil }
func (m *fakeMsg) InProgress() error                         { return nil }
func (m *fakeMsg) Term() error                               { m.terms++; return nil }
func (m *fakeMsg) TermWithReason(string) error               { m.terms++; return nil }

var _ jetstream.Msg = (*fakeMsg)(nil)

// mustEvent marshals a JobEvent fixture; a marshal failure is a broken test,
// not a test outcome.
func mustEvent(t *testing.T, e queue.JobEvent) []byte {
	t.Helper()
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	return b
}

// TestHandleJobEventFilteredMessagesAreAcked pins the disposition of every
// pre-persistence filter: malformed and non-actionable events are ACKED (not
// NAK'd) so they never redeliver — retrying an event that can never become a
// notification would only burn the redelivery budget and eventually spam the
// DLQ. models.DB is nil in unit tests, so a passing row simultaneously proves
// the filter fired BEFORE the idempotency query; reaching it would panic.
//
// The persistence paths (JobCompleted/JobFailed with valid ids, including the
// exact title/body/type mapping) write to models.DB inside the same function
// and are therefore integration-tier by construction.
func TestHandleJobEventFilteredMessagesAreAcked(t *testing.T) {
	uid := uuid.Must(uuid.NewV7()).String()
	jid := uuid.Must(uuid.NewV7()).String()
	cases := []struct {
		name string
		data []byte
	}{
		{"malformed json", []byte(`{not-json`)},
		{"empty payload", []byte(``)},
		{"guest event: no user id", mustEvent(t, queue.JobEvent{EventType: "JobCompleted", JobID: jid})},
		{"guest event: whitespace user id", mustEvent(t, queue.JobEvent{EventType: "JobCompleted", JobID: jid, UserID: "   "})},
		{"unparsable user id", mustEvent(t, queue.JobEvent{EventType: "JobCompleted", JobID: jid, UserID: "not-a-uuid"})},
		{"unparsable job id", mustEvent(t, queue.JobEvent{EventType: "JobCompleted", JobID: "not-a-uuid", UserID: uid})},
		{"unmapped event type", mustEvent(t, queue.JobEvent{EventType: "JobProgress", JobID: jid, UserID: uid})},
		{"empty event type", mustEvent(t, queue.JobEvent{JobID: jid, UserID: uid})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := &fakeMsg{data: tc.data, subject: "jobs.events.test"}
			handleJobEvent(context.Background(), msg)
			if msg.acks != 1 || msg.naks != 0 || msg.terms != 0 {
				t.Errorf("disposition = %d acks / %d naks / %d terms, want exactly 1 ack",
					msg.acks, msg.naks, msg.terms)
			}
		})
	}
}

// TestNakOrDLQ pins the retry/park boundary: below maxDeliver a failure NAKs
// for redelivery; at maxDeliver the message is Terminated so it cannot loop as
// a poison message (with natsconn.JS nil the DLQ copy is skipped, but the Term
// must still happen — parking is about stopping redelivery, not about the
// copy). An unreadable Metadata means the delivery count is unknown, and the
// safe reading of unknown is "retry", never "drop".
func TestNakOrDLQ(t *testing.T) {
	oldJS := natsconn.JS
	natsconn.JS = nil
	t.Cleanup(func() { natsconn.JS = oldJS })

	cause := errors.New("db down")
	t.Run("below max deliveries naks", func(t *testing.T) {
		msg := &fakeMsg{meta: &jetstream.MsgMetadata{NumDelivered: maxDeliver - 1}}
		nakOrDLQ(context.Background(), msg, "notification", cause)
		if msg.naks != 1 || msg.terms != 0 || msg.acks != 0 {
			t.Errorf("disposition = %d acks / %d naks / %d terms, want exactly 1 nak", msg.acks, msg.naks, msg.terms)
		}
	})
	t.Run("at max deliveries terms", func(t *testing.T) {
		msg := &fakeMsg{meta: &jetstream.MsgMetadata{NumDelivered: maxDeliver}}
		nakOrDLQ(context.Background(), msg, "notification", cause)
		if msg.terms != 1 || msg.naks != 0 || msg.acks != 0 {
			t.Errorf("disposition = %d acks / %d naks / %d terms, want exactly 1 term", msg.acks, msg.naks, msg.terms)
		}
	})
	t.Run("metadata error naks", func(t *testing.T) {
		msg := &fakeMsg{metaErr: errors.New("no metadata")}
		nakOrDLQ(context.Background(), msg, "notification", cause)
		if msg.naks != 1 || msg.terms != 0 {
			t.Errorf("disposition = %d naks / %d terms, want exactly 1 nak", msg.naks, msg.terms)
		}
	})
}

// TestEventBackoffFitsMaxDeliver is the drift guard between the two consumer
// tuning values passed to CreateOrUpdateConsumer in Start: JetStream rejects a
// consumer whose BackOff table is longer than MaxDeliver allows, so growing
// eventBackoff without raising maxDeliver would break the subscriber at
// startup, not at compile time.
func TestEventBackoffFitsMaxDeliver(t *testing.T) {
	if len(eventBackoff) >= maxDeliver {
		t.Errorf("len(eventBackoff)=%d must stay below maxDeliver=%d", len(eventBackoff), maxDeliver)
	}
}

// fakeConsumeContext is a hand-rolled jetstream.ConsumeContext double, the
// same shape as analytics-service's (duplicated by design — cross-service test
// helpers are forbidden, CLAUDE.md §1/§2).
type fakeConsumeContext struct {
	stopped int
	closed  chan struct{}
}

func newFakeConsumeContext() *fakeConsumeContext {
	return &fakeConsumeContext{closed: make(chan struct{})}
}

func (f *fakeConsumeContext) Stop() {
	f.stopped++
	select {
	case <-f.closed:
	default:
		close(f.closed)
	}
}
func (f *fakeConsumeContext) Drain()                  { f.Stop() }
func (f *fakeConsumeContext) Closed() <-chan struct{} { return f.closed }

var _ jetstream.ConsumeContext = (*fakeConsumeContext)(nil)

// TestSubscribersStopNilReceiver pins that shutdown is safe when Start never
// ran (e.g. NATS was down at boot) — main's deferred Stop must not panic.
func TestSubscribersStopNilReceiver(t *testing.T) {
	var s *Subscribers
	s.Stop()
}

// TestSubscribersStopIdempotent pins that a double Stop (signal race at
// shutdown) stops the consumer exactly once and clears the handle.
func TestSubscribersStopIdempotent(t *testing.T) {
	cc := newFakeConsumeContext()
	s := &Subscribers{jobs: cc}
	s.Stop()
	s.Stop()
	if cc.stopped != 1 {
		t.Errorf("consumer Stop called %d times, want 1", cc.stopped)
	}
	if s.jobs != nil {
		t.Error("Stop must clear the consumer handle so later calls are no-ops")
	}
}

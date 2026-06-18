package worker

import (
	"testing"
	"time"
)

// newTestThrottle builds a throttle with a controllable clock for deterministic
// tests, bypassing env lookups.
func newTestThrottle(startPct, minDelta int, minInterval time.Duration, clock *time.Time) *progressThrottle {
	return &progressThrottle{
		minDelta:    minDelta,
		minInterval: minInterval,
		lastPct:     startPct,
		lastWrite:   *clock,
		now:         func() time.Time { return *clock },
	}
}

func TestThrottleWritesOnDelta(t *testing.T) {
	clock := time.Unix(0, 0)
	th := newTestThrottle(20, 10, time.Hour, &clock)

	if th.shouldWrite(25) {
		t.Error("delta 5 < 10 should be suppressed")
	}
	if !th.shouldWrite(30) {
		t.Error("delta 10 should write")
	}
	// Baseline advanced to 30; 35 is only +5 and time hasn't moved.
	if th.shouldWrite(35) {
		t.Error("delta 5 after write should be suppressed")
	}
}

func TestThrottleWritesOnInterval(t *testing.T) {
	clock := time.Unix(0, 0)
	th := newTestThrottle(20, 50, 10*time.Second, &clock)

	if th.shouldWrite(21) {
		t.Error("delta 1 within interval should be suppressed")
	}
	clock = clock.Add(10 * time.Second)
	if !th.shouldWrite(22) {
		t.Error("interval elapsed should force a write even on tiny delta")
	}
}

func TestThrottleConcurrentSafe(t *testing.T) {
	clock := time.Unix(0, 0)
	th := newTestThrottle(0, 1, time.Hour, &clock)
	done := make(chan struct{})
	for i := 0; i < 20; i++ {
		go func() {
			th.shouldWrite(50)
			done <- struct{}{}
		}()
	}
	for i := 0; i < 20; i++ {
		<-done
	}
	close(done)
}

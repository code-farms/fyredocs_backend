package worker

import (
	"sync"
	"testing"
	"time"
)

func TestWaitDrainCompletesWithinDeadline(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		time.Sleep(20 * time.Millisecond)
		wg.Done()
	}()
	fired := false
	if ok := waitDrain(&wg, time.Second, func() { fired = true }); !ok {
		t.Error("expected drain to complete within deadline")
	}
	if fired {
		t.Error("onTimeout must not fire when drain completes in time")
	}
}

func TestWaitDrainTimesOutAndCancels(t *testing.T) {
	var wg sync.WaitGroup
	release := make(chan struct{})
	wg.Add(1)
	go func() {
		<-release // simulates a hung job until onTimeout "cancels" it
		wg.Done()
	}()
	ok := waitDrain(&wg, 20*time.Millisecond, func() { close(release) })
	if ok {
		t.Error("expected drain to report timeout")
	}
	// waitDrain must still have waited for the goroutine to unwind.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("waitDrain returned before the waitgroup unwound")
	}
}

func TestDrainTimeoutEnv(t *testing.T) {
	t.Setenv("WORKER_DRAIN_TIMEOUT", "")
	if got := drainTimeout(); got != 120*time.Second {
		t.Errorf("default drain timeout = %v, want 120s", got)
	}
	t.Setenv("WORKER_DRAIN_TIMEOUT", "45s")
	if got := drainTimeout(); got != 45*time.Second {
		t.Errorf("drain timeout = %v, want 45s", got)
	}
	t.Setenv("WORKER_DRAIN_TIMEOUT", "garbage")
	if got := drainTimeout(); got != 120*time.Second {
		t.Errorf("invalid value should fall back to 120s, got %v", got)
	}
}

package worker

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// NOTE: duplicated across the four PDF workers by design (CLAUDE.md §1).
//
// The two waitDrain tests run inside a synctest bubble: waitDrain is pure
// channel/timer plumbing, so under fake time the sleep-vs-deadline ordering
// is exact. With real clocks, a 20ms sleep on a loaded runner (especially
// under -race) can overshoot the drain deadline and flake.
func TestWaitDrainCompletesWithinDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
	})
}

func TestWaitDrainTimesOutAndCancels(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var wg sync.WaitGroup
		release := make(chan struct{})
		wg.Add(1)
		go func() {
			<-release // simulates a hung job until onTimeout "cancels" it
			wg.Done()
		}()
		if ok := waitDrain(&wg, 20*time.Millisecond, func() { close(release) }); ok {
			t.Error("expected drain to report timeout")
		}
		// waitDrain's contract is to return only after the waitgroup unwinds.
		// If it returned early, this Wait would block forever and synctest
		// would fail the bubble as deadlocked instead of hanging the run.
		wg.Wait()
	})
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

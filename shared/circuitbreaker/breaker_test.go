package circuitbreaker

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestBreakerPassesThroughSuccess(t *testing.T) {
	t.Parallel()
	b := New[int]("test-success")
	got, err := b.Execute(func() (int, error) { return 42, nil })
	if err != nil || got != 42 {
		t.Fatalf("Execute = (%d, %v), want (42, nil)", got, err)
	}
}

func TestBreakerTripsAndFailsFast(t *testing.T) {
	t.Parallel()
	b := New[int]("test-trip")
	boom := errors.New("boom")

	// 5 consecutive failures trip the breaker (ReadyToTrip threshold).
	for i := 0; i < 5; i++ {
		if _, err := b.Execute(func() (int, error) { return 0, boom }); err == nil {
			t.Fatalf("call %d: expected error", i)
		}
	}

	// Now OPEN: the next call must fail fast without invoking fn.
	called := false
	_, err := b.Execute(func() (int, error) { called = true; return 0, nil })
	if !errors.Is(err, ErrOpenState) {
		t.Fatalf("expected ErrOpenState when open, got %v", err)
	}
	if called {
		t.Fatal("fn must not be invoked while the breaker is open")
	}
}

// ─── State-transition table ───────────────────────────────────────────────────
//
// The transitions below run inside a synctest bubble: gobreaker decides
// open→half-open by comparing time.Now() against its 30s Timeout, and synctest
// virtualises the clock, so time.Sleep(31s) elapses instantly instead of being
// a real sleep (testing-strategy.md forbids timing assertions on real clocks).

// TestBreakerStateTransitions drives the full lifecycle against the package's
// real tuning (trip at 5 consecutive failures, 30s open→half-open, 1 probe):
// closed→open, half-open probe allowed, reopen on probe failure, close on
// probe success, and a fresh failure window after closing.
func TestBreakerStateTransitions(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		b := New[int]("test-transitions")
		boom := errors.New("boom")
		fail := func() (int, error) { return 0, boom }
		succeed := func() (int, error) { return 7, nil }

		// closed → open: all 5 tripping failures still invoke fn and surface
		// the real error — the breaker only fails fast AFTER the threshold.
		for i := 0; i < 5; i++ {
			if _, err := b.Execute(fail); !errors.Is(err, boom) {
				t.Fatalf("failure %d: err = %v, want boom (breaker must still be closed)", i+1, err)
			}
		}
		if _, err := b.Execute(succeed); !errors.Is(err, ErrOpenState) {
			t.Fatalf("after 5 failures: err = %v, want ErrOpenState", err)
		}

		// open → half-open: after Timeout one probe is let through; a FAILED
		// probe reopens immediately (the very next call fails fast again).
		time.Sleep(31 * time.Second)
		probed := false
		if _, err := b.Execute(func() (int, error) { probed = true; return 0, boom }); !errors.Is(err, boom) {
			t.Fatalf("half-open probe: err = %v, want boom", err)
		}
		if !probed {
			t.Fatal("half-open must let exactly one probe invoke fn")
		}
		if _, err := b.Execute(succeed); !errors.Is(err, ErrOpenState) {
			t.Fatalf("after failed probe: err = %v, want ErrOpenState (reopened)", err)
		}

		// open → half-open → closed: a SUCCESSFUL probe closes the breaker
		// (MaxRequests=1, so one success is enough).
		time.Sleep(31 * time.Second)
		if got, err := b.Execute(succeed); err != nil || got != 7 {
			t.Fatalf("successful probe = (%d, %v), want (7, nil)", got, err)
		}

		// Closed again with a fresh window: a single failure must not retrip.
		if _, err := b.Execute(fail); !errors.Is(err, boom) {
			t.Fatalf("post-close failure: err = %v, want boom", err)
		}
		if got, err := b.Execute(succeed); err != nil || got != 7 {
			t.Fatalf("post-close success = (%d, %v), want (7, nil) — one failure must not retrip", got, err)
		}
	})
}

// TestTwoStepBreakerTransitions covers the Allow/done surface (used where the
// work streams to a ResponseWriter and cannot be wrapped in Execute): reported
// failures trip it exactly like Execute failures, Allow fails fast while open,
// and a successful probe after Timeout closes it.
func TestTwoStepBreakerTransitions(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ts := NewTwoStep("test-twostep")
		boom := errors.New("boom")

		for i := 0; i < 5; i++ {
			done, err := ts.Allow()
			if err != nil {
				t.Fatalf("Allow %d: err = %v, want nil (still closed)", i+1, err)
			}
			done(boom)
		}
		if _, err := ts.Allow(); !errors.Is(err, ErrOpenState) {
			t.Fatalf("after 5 reported failures: err = %v, want ErrOpenState", err)
		}

		time.Sleep(31 * time.Second)
		done, err := ts.Allow()
		if err != nil {
			t.Fatalf("half-open probe: err = %v, want nil", err)
		}
		done(nil)

		// Probe success closed it: subsequent calls flow again.
		done2, err := ts.Allow()
		if err != nil {
			t.Fatalf("after successful probe: err = %v, want nil (closed)", err)
		}
		done2(nil)
	})
}

package circuitbreaker

import (
	"errors"
	"testing"
)

func TestBreakerPassesThroughSuccess(t *testing.T) {
	b := New[int]("test-success")
	got, err := b.Execute(func() (int, error) { return 42, nil })
	if err != nil || got != 42 {
		t.Fatalf("Execute = (%d, %v), want (42, nil)", got, err)
	}
}

func TestBreakerTripsAndFailsFast(t *testing.T) {
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

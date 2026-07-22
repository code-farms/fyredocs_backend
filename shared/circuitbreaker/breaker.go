// Package circuitbreaker wraps sony/gobreaker with sane defaults so callers can
// protect a flaky/slow dependency (an internal HTTP client, an upstream proxy)
// without every request paying the full timeout once the dependency is down.
//
// When the breaker is OPEN it fails fast with gobreaker.ErrOpenState; after
// Timeout it half-opens and lets a probe through. State changes are logged.
package circuitbreaker

import (
	"log/slog"
	"time"

	"github.com/sony/gobreaker/v2"
)

// settings returns the shared breaker tuning: trip after 5 consecutive failures,
// probe again after 30s, log state changes. `name` identifies it in logs.
func settings(name string) gobreaker.Settings {
	return gobreaker.Settings{
		Name:        name,
		MaxRequests: 1,                // one probe request while half-open
		Interval:    60 * time.Second, // reset the consecutive-failure window when closed
		Timeout:     30 * time.Second, // open → half-open after this
		ReadyToTrip: func(c gobreaker.Counts) bool { return c.ConsecutiveFailures >= 5 },
		OnStateChange: func(name string, from, to gobreaker.State) {
			slog.Warn("circuit breaker state change",
				"op", "circuitbreaker.state", "name", name, "from", from.String(), "to", to.String())
		},
	}
}

// Breaker guards calls returning a value of type T plus an error.
type Breaker[T any] struct {
	cb *gobreaker.CircuitBreaker[T]
}

// New returns a breaker that trips after 5 consecutive failures and probes again
// after 30s. `name` identifies it in state-change logs.
func New[T any](name string) *Breaker[T] {
	return &Breaker[T]{cb: gobreaker.NewCircuitBreaker[T](settings(name))}
}

// Execute runs fn through the breaker. When open it returns gobreaker.ErrOpenState
// immediately (fail fast) instead of invoking fn.
func (b *Breaker[T]) Execute(fn func() (T, error)) (T, error) {
	return b.cb.Execute(fn)
}

// TwoStep guards work that doesn't fit a func()(T,error) — e.g. an HTTP reverse
// proxy that streams straight to the ResponseWriter. Call Allow() before the
// work; if it returns an error the breaker is open (fail fast, skip the work);
// otherwise run the work and call the returned done(success) with the outcome.
type TwoStep struct {
	cb *gobreaker.TwoStepCircuitBreaker[any]
}

// NewTwoStep returns a TwoStep breaker with the shared tuning.
func NewTwoStep(name string) *TwoStep {
	return &TwoStep{cb: gobreaker.NewTwoStepCircuitBreaker[any](settings(name))}
}

// Allow reports whether a call may proceed. On success it returns a done func
// that must be called with the outcome (nil = success, non-nil = failure);
// when open it returns ErrOpenState.
func (t *TwoStep) Allow() (done func(error), err error) {
	return t.cb.Allow()
}

// ErrOpenState is re-exported so callers can detect a fail-fast without importing gobreaker.
var ErrOpenState = gobreaker.ErrOpenState

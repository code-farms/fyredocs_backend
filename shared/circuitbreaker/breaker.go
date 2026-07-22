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

// Breaker guards calls returning a value of type T plus an error.
type Breaker[T any] struct {
	cb *gobreaker.CircuitBreaker[T]
}

// New returns a breaker that trips after 5 consecutive failures and probes again
// after 30s. `name` identifies it in state-change logs.
func New[T any](name string) *Breaker[T] {
	return &Breaker[T]{cb: gobreaker.NewCircuitBreaker[T](gobreaker.Settings{
		Name:        name,
		MaxRequests: 1,                // one probe request while half-open
		Interval:    60 * time.Second, // reset the consecutive-failure window when closed
		Timeout:     30 * time.Second, // open → half-open after this
		ReadyToTrip: func(c gobreaker.Counts) bool { return c.ConsecutiveFailures >= 5 },
		OnStateChange: func(name string, from, to gobreaker.State) {
			slog.Warn("circuit breaker state change",
				"op", "circuitbreaker.state", "name", name, "from", from.String(), "to", to.String())
		},
	})}
}

// Execute runs fn through the breaker. When open it returns gobreaker.ErrOpenState
// immediately (fail fast) instead of invoking fn.
func (b *Breaker[T]) Execute(fn func() (T, error)) (T, error) {
	return b.cb.Execute(fn)
}

// ErrOpenState is re-exported so callers can detect a fail-fast without importing gobreaker.
var ErrOpenState = gobreaker.ErrOpenState

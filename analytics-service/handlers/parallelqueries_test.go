package handlers

import (
	"sync/atomic"
	"testing"
)

// parallelQueries must run every function exactly once and block until all of
// them have finished (the dashboard handlers rely on every result variable being
// populated before they build the response).

func TestParallelQueriesRunsAllAndWaits(t *testing.T) {
	const n = 12
	var ran int64
	fns := make([]func(), n)
	for i := range fns {
		fns[i] = func() { atomic.AddInt64(&ran, 1) }
	}

	parallelQueries(fns...)

	if got := atomic.LoadInt64(&ran); got != n {
		t.Fatalf("expected all %d functions to run before return, got %d", n, got)
	}
}

func TestParallelQueriesEmpty(t *testing.T) {
	// Must not panic or block when given no work.
	parallelQueries()
}

func TestParallelQueriesWritesDistinctVars(t *testing.T) {
	var a, b, c int
	parallelQueries(
		func() { a = 1 },
		func() { b = 2 },
		func() { c = 3 },
	)
	if a != 1 || b != 2 || c != 3 {
		t.Fatalf("expected a=1 b=2 c=3, got a=%d b=%d c=%d", a, b, c)
	}
}

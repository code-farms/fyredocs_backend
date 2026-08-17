package activity

import (
	"context"
	"testing"
	"time"
)

// TestStartRetention_DisabledExitsImmediately proves ACTIVITY_RETENTION_DAYS<=0
// is a real off switch: the goroutine closes its done channel without ever
// touching the (nil) DB handle — reaching a query would panic.
func TestStartRetention_DisabledExitsImmediately(t *testing.T) {
	t.Setenv("ACTIVITY_RETENTION_DAYS", "0")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := StartRetention(ctx, nil)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("disabled retention goroutine did not exit")
	}
}

// TestStartRetention_CancelBeforeFirstSweep verifies the shutdown join
// contract: cancelling the context releases the goroutine without a sweep
// (the first sweep is a minute out; a nil DB would panic if one ran).
func TestStartRetention_CancelBeforeFirstSweep(t *testing.T) {
	t.Setenv("ACTIVITY_RETENTION_DAYS", "180")
	ctx, cancel := context.WithCancel(context.Background())

	done := StartRetention(ctx, nil)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("retention goroutine did not honour context cancellation")
	}
}

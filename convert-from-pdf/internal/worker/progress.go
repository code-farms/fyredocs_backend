package worker

import (
	"sync"
	"time"

	"fyredocs/shared/config"
)

// progressThrottle decides when a progress percentage is worth persisting to
// the database. Workers receive frequent progress callbacks; writing every one
// produces many DB updates per job. NATS progress events remain cheap and are
// always published for a smooth SSE progress bar, but DB writes only advance
// when progress jumps by at least PROGRESS_DB_MIN_DELTA percent or at least
// PROGRESS_DB_MIN_INTERVAL has elapsed since the last write.
type progressThrottle struct {
	mu          sync.Mutex
	minDelta    int
	minInterval time.Duration
	lastPct     int
	lastWrite   time.Time
	now         func() time.Time
}

// newProgressThrottle seeds the throttle at startPct (the progress value the
// caller has already written) so the first callback isn't forced to write.
func newProgressThrottle(startPct int) *progressThrottle {
	now := time.Now
	return &progressThrottle{
		minDelta:    config.GetEnvInt("PROGRESS_DB_MIN_DELTA", 10),
		minInterval: config.GetEnvDuration("PROGRESS_DB_MIN_INTERVAL", 10*time.Second),
		lastPct:     startPct,
		lastWrite:   now(),
		now:         now,
	}
}

// shouldWrite reports whether pct warrants a DB write now. When it returns true
// it records pct/time as the new baseline; the no-regress guard on the UPDATE
// itself still protects against out-of-order writes.
func (t *progressThrottle) shouldWrite(pct int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if pct-t.lastPct >= t.minDelta || now.Sub(t.lastWrite) >= t.minInterval {
		t.lastPct = pct
		t.lastWrite = now
		return true
	}
	return false
}

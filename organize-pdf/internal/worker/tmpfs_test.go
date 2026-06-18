package worker

import (
	"context"
	"errors"
	"testing"
)

func TestProjectedFootprintDefaultFactor(t *testing.T) {
	t.Setenv("TMPFS_OUTPUT_FACTOR_PCT", "")
	// Default 200% → peak = input + 2*input = 3*input.
	if got := projectedFootprint(100 * bytesPerMiB); got != 300*bytesPerMiB {
		t.Errorf("projectedFootprint(100MiB) = %d, want %d", got, 300*bytesPerMiB)
	}
}

func TestProjectedFootprintCustomFactor(t *testing.T) {
	t.Setenv("TMPFS_OUTPUT_FACTOR_PCT", "50")
	// 50% → peak = input + 0.5*input = 1.5*input.
	if got := projectedFootprint(200 * bytesPerMiB); got != 300*bytesPerMiB {
		t.Errorf("projectedFootprint(200MiB, 50%%) = %d, want %d", got, 300*bytesPerMiB)
	}
}

func TestBudgetAndThresholdEnv(t *testing.T) {
	t.Setenv("TMPFS_BUDGET_MB", "500")
	if got := tmpfsBudgetBytes(); got != 500*bytesPerMiB {
		t.Errorf("tmpfsBudgetBytes = %d, want %d", got, 500*bytesPerMiB)
	}
	t.Setenv("LARGE_JOB_THRESHOLD_MB", "10")
	if got := largeJobThresholdBytes(); got != 10*bytesPerMiB {
		t.Errorf("largeJobThresholdBytes = %d, want %d", got, 10*bytesPerMiB)
	}
}

func TestTotalInputSizeSumsKeys(t *testing.T) {
	store := newFakeStorage()
	store.objectSize = 2048
	total, err := totalInputSize(context.Background(), store, []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 3*2048 {
		t.Errorf("totalInputSize = %d, want %d", total, 3*2048)
	}
}

func TestTotalInputSizePropagatesStatError(t *testing.T) {
	store := newFakeStorage()
	store.statErr = errors.New("boom")
	if _, err := totalInputSize(context.Background(), store, []string{"a"}); err == nil {
		t.Fatal("expected stat error to propagate")
	}
}

func TestLargeJobSemaphoreSerializes(t *testing.T) {
	// Acquire the single slot; a second non-blocking acquire must fail.
	largeJobSem <- struct{}{}
	select {
	case largeJobSem <- struct{}{}:
		<-largeJobSem // drain to avoid leaking into other tests
		t.Fatal("large-job semaphore should only admit one holder")
	default:
		// expected: full
	}
	<-largeJobSem // release
}

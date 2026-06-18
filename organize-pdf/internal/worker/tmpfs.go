package worker

import (
	"context"

	"fyredocs/shared/config"
)

// Workers download inputs into a per-job scratch directory on a bounded tmpfs
// (1 GiB by default). Without a guard, a large file — or two concurrent
// large jobs — can exhaust the scratch area mid-write and fail with ENOSPC or
// OOM. These helpers reject jobs that cannot physically fit and serialize large
// jobs within a single pod.

const bytesPerMiB = 1 << 20

// largeJobSem serializes "large" jobs within this worker pod so two of them
// never run concurrently and exhaust the shared scratch tmpfs. Sized 1: at most
// one large job processes at a time per pod (small jobs are unaffected).
var largeJobSem = make(chan struct{}, 1)

// tmpfsBudgetBytes is the maximum projected scratch footprint a single job may
// use, derived from TMPFS_BUDGET_MB (default 900, leaving headroom under a
// 1 GiB tmpfs).
func tmpfsBudgetBytes() int64 {
	return int64(config.GetEnvInt("TMPFS_BUDGET_MB", 900)) * bytesPerMiB
}

// largeJobThresholdBytes is the input-size above which a job must take the
// large-job semaphore, from LARGE_JOB_THRESHOLD_MB (default 100).
func largeJobThresholdBytes() int64 {
	return int64(config.GetEnvInt("LARGE_JOB_THRESHOLD_MB", 100)) * bytesPerMiB
}

// projectedFootprint estimates peak scratch usage for a job: the input files
// stay on disk while the output is written, and an output can be larger than
// its input (e.g. image-based conversions). TMPFS_OUTPUT_FACTOR_PCT (default
// 200 = output up to 2x input) controls the multiplier; peak = input * (1 + f).
func projectedFootprint(totalInput int64) int64 {
	pct := config.GetEnvInt("TMPFS_OUTPUT_FACTOR_PCT", 200)
	if pct < 0 {
		pct = 0
	}
	return totalInput + totalInput*int64(pct)/100
}

// totalInputSize sums the sizes of every input object key in the uploads
// bucket. A stat error is returned to the caller (treated as recoverable).
func totalInputSize(ctx context.Context, store Storage, keys []string) (int64, error) {
	var total int64
	for _, key := range keys {
		size, err := store.GetObjectSize(ctx, store.BucketUploads(), key)
		if err != nil {
			return 0, err
		}
		total += size
	}
	return total, nil
}

package queue

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"fyredocs/shared/metrics"
)

// recordTerminalJobMetric must emit jobs_processed_total on JobCompleted and
// jobs_failed_total on JobFailed (keyed by tool_type), and ignore every
// non-terminal / dispatch event.
func TestRecordTerminalJobMetric(t *testing.T) {
	completed := metrics.JobsProcessed.WithLabelValues("word-to-pdf", "completed")
	before := testutil.ToFloat64(completed)
	recordTerminalJobMetric(JobEvent{EventType: "JobCompleted", ToolType: "word-to-pdf"})
	if delta := testutil.ToFloat64(completed) - before; delta != 1 {
		t.Errorf("JobCompleted: jobs_processed_total delta = %v, want 1", delta)
	}

	failed := metrics.JobsFailed.WithLabelValues("merge-pdf", "error")
	fbefore := testutil.ToFloat64(failed)
	recordTerminalJobMetric(JobEvent{EventType: "JobFailed", ToolType: "merge-pdf"})
	if delta := testutil.ToFloat64(failed) - fbefore; delta != 1 {
		t.Errorf("JobFailed: jobs_failed_total delta = %v, want 1", delta)
	}

	// Non-terminal events must not move the counters.
	prog := metrics.JobsProcessed.WithLabelValues("split-pdf", "completed")
	pbefore := testutil.ToFloat64(prog)
	recordTerminalJobMetric(JobEvent{EventType: "JobProgress", ToolType: "split-pdf"})
	recordTerminalJobMetric(JobEvent{EventType: "JobQueued", ToolType: "split-pdf"})
	if delta := testutil.ToFloat64(prog) - pbefore; delta != 0 {
		t.Errorf("non-terminal events: jobs_processed_total delta = %v, want 0", delta)
	}
}

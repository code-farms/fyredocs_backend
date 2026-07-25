package queue

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"

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

func TestRecordTerminalJobMetricObservesDuration(t *testing.T) {
	histogramCount := func(tool, status string) uint64 {
		h, err := metrics.JobDuration.MetricVec.GetMetricWithLabelValues(tool, status)
		if err != nil {
			t.Fatal(err)
		}
		m := &dto.Metric{}
		if err := h.(prometheus.Histogram).Write(m); err != nil {
			t.Fatal(err)
		}
		return m.GetHistogram().GetSampleCount()
	}

	now := time.Now().UTC()

	// Terminal event with QueuedAt → one observation.
	before := histogramCount("ocr-pdf", "completed")
	recordTerminalJobMetric(JobEvent{
		EventType: "JobCompleted", ToolType: "ocr-pdf",
		QueuedAt: now.Add(-42 * time.Second), Timestamp: now,
	})
	if got := histogramCount("ocr-pdf", "completed") - before; got != 1 {
		t.Errorf("with QueuedAt: sample count delta = %d, want 1", got)
	}

	// Zero QueuedAt (legacy events) → no observation.
	before = histogramCount("ocr-pdf", "failed")
	recordTerminalJobMetric(JobEvent{EventType: "JobFailed", ToolType: "ocr-pdf", Timestamp: now})
	if got := histogramCount("ocr-pdf", "failed") - before; got != 0 {
		t.Errorf("zero QueuedAt: sample count delta = %d, want 0", got)
	}

	// Negative duration (clock skew) → skipped.
	before = histogramCount("ocr-pdf", "completed")
	recordTerminalJobMetric(JobEvent{
		EventType: "JobCompleted", ToolType: "ocr-pdf",
		QueuedAt: now.Add(time.Hour), Timestamp: now,
	})
	if got := histogramCount("ocr-pdf", "completed") - before; got != 0 {
		t.Errorf("negative duration: sample count delta = %d, want 0", got)
	}
}

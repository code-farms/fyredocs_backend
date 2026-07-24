package opsmetrics

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestSetDependency(t *testing.T) {
	setDependency("redis", true)
	if v := testutil.ToFloat64(dependencyUp.WithLabelValues("redis")); v != 1 {
		t.Errorf("dependency_up{redis} = %v, want 1", v)
	}
	setDependency("redis", false)
	if v := testutil.ToFloat64(dependencyUp.WithLabelValues("redis")); v != 0 {
		t.Errorf("dependency_up{redis} = %v, want 0", v)
	}
	setDependency("postgres", true)
	if v := testutil.ToFloat64(dependencyUp.WithLabelValues("postgres")); v != 1 {
		t.Errorf("dependency_up{postgres} = %v, want 1", v)
	}
}

func TestSetDLQ(t *testing.T) {
	setDLQ(42)
	if v := testutil.ToFloat64(dlqPending); v != 42 {
		t.Errorf("nats_dlq_pending_messages = %v, want 42", v)
	}
}

func TestBoolGauge(t *testing.T) {
	if boolGauge(true) != 1 || boolGauge(false) != 0 {
		t.Error("boolGauge mapping wrong")
	}
}

// TestSampleNoDepsMarksDown verifies sample() is safe with nil deps and marks
// them down rather than panicking (redisstore.Client / natsconn are unset in
// unit tests).
func TestSampleNoDepsMarksDown(t *testing.T) {
	sample(context.Background(), nil)
	if v := testutil.ToFloat64(dependencyUp.WithLabelValues("postgres")); v != 0 {
		t.Errorf("postgres should be down with nil db, got %v", v)
	}
	if v := testutil.ToFloat64(dependencyUp.WithLabelValues("redis")); v != 0 {
		t.Errorf("redis should be down with nil client, got %v", v)
	}
}

// TestStartStopsOnCancel ensures Start's goroutine honors ctx cancellation
// (no hang / leak).
func TestStartStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	Start(ctx, nil)
	// If Start's loop ignored ctx it would tick forever; nothing to assert
	// beyond the test returning promptly (go test would time out otherwise).
}

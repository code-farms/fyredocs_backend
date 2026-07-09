package apisampler

import (
	"testing"

	"analytics-service/internal/promscrape"
)

func TestComputeSampleDeltas_Normal(t *testing.T) {
	prev := promscrape.HTTPAggregate{Requests: 100, ClientErrors: 5, ServerErrors: 2, Timeouts: 1}
	cur := promscrape.HTTPAggregate{Requests: 130, ClientErrors: 8, ServerErrors: 2, Timeouts: 3, P95Ms: 42}
	s := computeSampleDeltas(prev, cur)
	if s.Requests != 30 {
		t.Errorf("Requests delta = %d, want 30", s.Requests)
	}
	if s.ClientErrors != 3 || s.ServerErrors != 0 || s.Timeouts != 2 {
		t.Errorf("error deltas = %d/%d/%d, want 3/0/2", s.ClientErrors, s.ServerErrors, s.Timeouts)
	}
	if s.P95Ms != 42 {
		t.Errorf("P95Ms = %f, want current snapshot 42", s.P95Ms)
	}
}

func TestComputeSampleDeltas_CounterReset(t *testing.T) {
	// Gateway restarted: current cumulative < previous → treat current as the delta.
	prev := promscrape.HTTPAggregate{Requests: 1000, ClientErrors: 50}
	cur := promscrape.HTTPAggregate{Requests: 12, ClientErrors: 1}
	s := computeSampleDeltas(prev, cur)
	if s.Requests != 12 {
		t.Errorf("Requests after reset = %d, want 12 (current)", s.Requests)
	}
	if s.ClientErrors != 1 {
		t.Errorf("ClientErrors after reset = %d, want 1", s.ClientErrors)
	}
}

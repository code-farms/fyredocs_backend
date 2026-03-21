package promscrape

import (
	"math"
	"testing"
)

func TestHistogramPercentile_EmptyBuckets(t *testing.T) {
	result := histogramPercentile(nil, 0, 0.5)
	if result != 0 {
		t.Errorf("expected 0 for empty buckets, got %f", result)
	}
}

func TestHistogramPercentile_SingleBucket(t *testing.T) {
	buckets := []bucketEntry{
		{UpperBound: 0.5, Count: 100},
		{UpperBound: math.Inf(1), Count: 100},
	}
	result := histogramPercentile(buckets, 100, 0.5)
	if result < 0 || result > 0.5 {
		t.Errorf("expected p50 between 0 and 0.5, got %f", result)
	}
}

func TestHistogramPercentile_MultipleBuckets(t *testing.T) {
	buckets := []bucketEntry{
		{UpperBound: 0.01, Count: 50},
		{UpperBound: 0.05, Count: 80},
		{UpperBound: 0.1, Count: 90},
		{UpperBound: 0.5, Count: 95},
		{UpperBound: 1.0, Count: 100},
		{UpperBound: math.Inf(1), Count: 100},
	}

	p50 := histogramPercentile(buckets, 100, 0.50)
	p95 := histogramPercentile(buckets, 100, 0.95)
	p99 := histogramPercentile(buckets, 100, 0.99)

	if p50 >= p95 {
		t.Errorf("p50 (%f) should be less than p95 (%f)", p50, p95)
	}
	if p95 >= p99 {
		t.Errorf("p95 (%f) should be less than p99 (%f)", p95, p99)
	}
}

func TestGaugeValue_Missing(t *testing.T) {
	result := GaugeValue(nil, "nonexistent")
	if result != 0 {
		t.Errorf("expected 0 for missing metric, got %f", result)
	}
}

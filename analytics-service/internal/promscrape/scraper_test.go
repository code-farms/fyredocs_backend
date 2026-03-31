package promscrape

import (
	"math"
	"testing"

	dto "github.com/prometheus/client_model/go"
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

func TestGaugeLabelValue(t *testing.T) {
	gaugeType := dto.MetricType_GAUGE
	metricName := "go_info"
	labelName := "version"
	labelValue := "go1.25.8"
	gaugeVal := float64(1)

	families := map[string]*dto.MetricFamily{
		"go_info": {
			Name: &metricName,
			Type: &gaugeType,
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: &labelName, Value: &labelValue},
					},
					Gauge: &dto.Gauge{Value: &gaugeVal},
				},
			},
		},
	}

	t.Run("found", func(t *testing.T) {
		got := GaugeLabelValue(families, "go_info", "version")
		if got != "go1.25.8" {
			t.Errorf("expected 'go1.25.8', got %q", got)
		}
	})

	t.Run("missing metric", func(t *testing.T) {
		got := GaugeLabelValue(families, "nonexistent", "version")
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})

	t.Run("missing label", func(t *testing.T) {
		got := GaugeLabelValue(families, "go_info", "nonexistent")
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})

	t.Run("nil families", func(t *testing.T) {
		got := GaugeLabelValue(nil, "go_info", "version")
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})
}

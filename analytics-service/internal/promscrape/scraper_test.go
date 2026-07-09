package promscrape

import (
	"math"
	"strings"
	"testing"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

const httpHistogramFixture = `# TYPE http_request_duration_seconds histogram
http_request_duration_seconds_bucket{method="GET",path="/a",status="200",le="0.1"} 8
http_request_duration_seconds_bucket{method="GET",path="/a",status="200",le="0.5"} 10
http_request_duration_seconds_bucket{method="GET",path="/a",status="200",le="+Inf"} 10
http_request_duration_seconds_sum{method="GET",path="/a",status="200"} 1.5
http_request_duration_seconds_count{method="GET",path="/a",status="200"} 10
http_request_duration_seconds_bucket{method="POST",path="/b",status="404",le="0.1"} 3
http_request_duration_seconds_bucket{method="POST",path="/b",status="404",le="+Inf"} 3
http_request_duration_seconds_sum{method="POST",path="/b",status="404"} 0.3
http_request_duration_seconds_count{method="POST",path="/b",status="404"} 3
http_request_duration_seconds_bucket{method="POST",path="/c",status="500",le="1.0"} 2
http_request_duration_seconds_bucket{method="POST",path="/c",status="500",le="+Inf"} 2
http_request_duration_seconds_sum{method="POST",path="/c",status="500"} 2.0
http_request_duration_seconds_count{method="POST",path="/c",status="500"} 2
http_request_duration_seconds_bucket{method="POST",path="/d",status="504",le="5.0"} 1
http_request_duration_seconds_bucket{method="POST",path="/d",status="504",le="+Inf"} 1
http_request_duration_seconds_sum{method="POST",path="/d",status="504"} 5.0
http_request_duration_seconds_count{method="POST",path="/d",status="504"} 1
`

func TestAggregateHTTP(t *testing.T) {
	var parser expfmt.TextParser
	families, err := parser.TextToMetricFamilies(strings.NewReader(httpHistogramFixture))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	agg := AggregateHTTP(families)

	if agg.Requests != 16 {
		t.Errorf("Requests = %d, want 16", agg.Requests)
	}
	if agg.ClientErrors != 3 {
		t.Errorf("ClientErrors = %d, want 3", agg.ClientErrors)
	}
	if agg.ServerErrors != 2 {
		t.Errorf("ServerErrors = %d, want 2 (5xx excluding 504)", agg.ServerErrors)
	}
	if agg.Timeouts != 1 {
		t.Errorf("Timeouts = %d, want 1 (504)", agg.Timeouts)
	}
	if agg.P50Ms <= 0 || agg.P95Ms < agg.P50Ms || agg.AvgMs <= 0 {
		t.Errorf("latency percentiles look wrong: avg=%f p50=%f p95=%f p99=%f", agg.AvgMs, agg.P50Ms, agg.P95Ms, agg.P99Ms)
	}
}

func TestAggregateHTTP_Empty(t *testing.T) {
	agg := AggregateHTTP(map[string]*dto.MetricFamily{})
	if agg.Requests != 0 || agg.P95Ms != 0 {
		t.Errorf("empty families should yield zero aggregate, got %+v", agg)
	}
}

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

package promscrape

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

// ScrapeTimeout is the default timeout for scraping a Prometheus endpoint.
const ScrapeTimeout = 5 * time.Second

// MetricFamilies fetches and parses all metric families from a Prometheus /metrics endpoint.
func MetricFamilies(ctx context.Context, url string) (map[string]*dto.MetricFamily, error) {
	reqCtx, cancel := context.WithTimeout(ctx, ScrapeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("scraping %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("scraping %s: status %d", url, resp.StatusCode)
	}

	var parser expfmt.TextParser
	families, err := parser.TextToMetricFamilies(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parsing metrics from %s: %w", url, err)
	}

	return families, nil
}

// GaugeValue returns the value of a gauge metric by name. Returns 0 if not found.
func GaugeValue(families map[string]*dto.MetricFamily, name string) float64 {
	fam, ok := families[name]
	if !ok || fam.GetType() != dto.MetricType_GAUGE {
		return 0
	}
	metrics := fam.GetMetric()
	if len(metrics) == 0 {
		return 0
	}
	return metrics[0].GetGauge().GetValue()
}

// HistogramEndpoint holds computed metrics for a single method+path+status combination.
type HistogramEndpoint struct {
	Method   string  `json:"method"`
	Path     string  `json:"path"`
	Requests float64 `json:"requests"`
	SumMs    float64 `json:"sumMs"`
	AvgMs    float64 `json:"avgMs"`
	P50Ms    float64 `json:"p50Ms"`
	P95Ms    float64 `json:"p95Ms"`
	P99Ms    float64 `json:"p99Ms"`
	Errors   float64 `json:"errors"`
}

// endpointKey groups by method+path.
type endpointKey struct {
	Method string
	Path   string
}

type bucketEntry struct {
	UpperBound float64
	Count      float64
}

// ParseHTTPHistogram extracts per-endpoint latency stats from the http_request_duration_seconds histogram.
func ParseHTTPHistogram(families map[string]*dto.MetricFamily) []HistogramEndpoint {
	fam, ok := families["http_request_duration_seconds"]
	if !ok || fam.GetType() != dto.MetricType_HISTOGRAM {
		return nil
	}

	// Aggregate by method+path across all status codes
	type aggData struct {
		totalCount float64
		totalSum   float64
		errors     float64
		buckets    []bucketEntry // merged from all statuses
	}

	agg := make(map[endpointKey]*aggData)

	for _, m := range fam.GetMetric() {
		labels := m.GetLabel()
		var method, path, status string
		for _, l := range labels {
			switch l.GetName() {
			case "method":
				method = l.GetValue()
			case "path":
				path = l.GetValue()
			case "status":
				status = l.GetValue()
			}
		}

		h := m.GetHistogram()
		if h == nil {
			continue
		}

		key := endpointKey{Method: method, Path: path}
		data, exists := agg[key]
		if !exists {
			data = &aggData{}
			agg[key] = data
		}

		count := float64(h.GetSampleCount())
		data.totalCount += count
		data.totalSum += h.GetSampleSum()

		// Count 4xx and 5xx as errors
		if len(status) > 0 && (status[0] == '4' || status[0] == '5') {
			data.errors += count
		}

		// Merge buckets (for percentile computation, use cumulative counts)
		for _, b := range h.GetBucket() {
			data.buckets = append(data.buckets, bucketEntry{
				UpperBound: b.GetUpperBound(),
				Count:      float64(b.GetCumulativeCount()),
			})
		}
	}

	results := make([]HistogramEndpoint, 0, len(agg))
	for key, data := range agg {
		if data.totalCount == 0 {
			continue
		}

		// Deduplicate and sort buckets by upper bound, summing counts for same bound
		bucketMap := make(map[float64]float64)
		for _, b := range data.buckets {
			bucketMap[b.UpperBound] += b.Count
		}
		sortedBuckets := make([]bucketEntry, 0, len(bucketMap))
		for ub, cnt := range bucketMap {
			sortedBuckets = append(sortedBuckets, bucketEntry{UpperBound: ub, Count: cnt})
		}
		sort.Slice(sortedBuckets, func(i, j int) bool {
			return sortedBuckets[i].UpperBound < sortedBuckets[j].UpperBound
		})

		ep := HistogramEndpoint{
			Method:   key.Method,
			Path:     key.Path,
			Requests: data.totalCount,
			SumMs:    data.totalSum * 1000,
			AvgMs:    (data.totalSum / data.totalCount) * 1000,
			P50Ms:    histogramPercentile(sortedBuckets, data.totalCount, 0.50) * 1000,
			P95Ms:    histogramPercentile(sortedBuckets, data.totalCount, 0.95) * 1000,
			P99Ms:    histogramPercentile(sortedBuckets, data.totalCount, 0.99) * 1000,
			Errors:   data.errors,
		}
		results = append(results, ep)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Requests > results[j].Requests
	})

	return results
}

// histogramPercentile estimates a percentile from histogram bucket data using linear interpolation.
func histogramPercentile(buckets []bucketEntry, total float64, q float64) float64 {
	if len(buckets) == 0 || total == 0 {
		return 0
	}

	target := q * total
	prevBound := 0.0
	prevCount := 0.0

	for _, b := range buckets {
		if math.IsInf(b.UpperBound, 1) {
			break
		}
		if b.Count >= target {
			// Linear interpolation within this bucket
			fraction := (target - prevCount) / (b.Count - prevCount)
			if b.Count == prevCount {
				return b.UpperBound
			}
			return prevBound + fraction*(b.UpperBound-prevBound)
		}
		prevBound = b.UpperBound
		prevCount = b.Count
	}

	// If we exhaust all buckets, return the last finite upper bound
	for i := len(buckets) - 1; i >= 0; i-- {
		if !math.IsInf(buckets[i].UpperBound, 1) {
			return buckets[i].UpperBound
		}
	}
	return 0
}

// CheckHealth pings a service's /healthz endpoint and returns whether it's healthy.
func CheckHealth(ctx context.Context, baseURL string) (bool, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, baseURL+"/healthz", nil)
	if err != nil {
		return false, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); resp.Body.Close() }()

	return resp.StatusCode == http.StatusOK, nil
}

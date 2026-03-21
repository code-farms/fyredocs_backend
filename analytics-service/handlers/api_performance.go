package handlers

import (
	"os"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"analytics-service/internal/promscrape"
	"esydocs/shared/response"
)

// APIPerformance returns per-endpoint latency, throughput, and error rate metrics
// by scraping the API gateway's Prometheus /metrics endpoint.
func APIPerformance(c *gin.Context) {
	ctx := c.Request.Context()

	gatewayURL := strings.TrimSpace(os.Getenv("API_GATEWAY_METRICS_URL"))
	if gatewayURL == "" {
		gatewayURL = "http://api-gateway:8080/metrics"
	}

	families, err := promscrape.MetricFamilies(ctx, gatewayURL)
	if err != nil {
		response.OK(c, "API performance unavailable", gin.H{
			"error": "Could not scrape API gateway metrics: " + err.Error(),
			"hint":  "Ensure the API gateway is running and accessible at " + gatewayURL,
		})
		return
	}

	endpoints := promscrape.ParseHTTPHistogram(families)
	if endpoints == nil {
		endpoints = []promscrape.HistogramEndpoint{}
	}

	// Compute summary across all endpoints
	var totalRequests, totalSumMs, totalErrors float64
	for _, ep := range endpoints {
		totalRequests += ep.Requests
		totalSumMs += ep.SumMs
		totalErrors += ep.Errors
	}

	var avgLatencyMs, errorRate float64
	if totalRequests > 0 {
		avgLatencyMs = totalSumMs / totalRequests
		errorRate = totalErrors / totalRequests
	}

	// Compute overall percentiles from all endpoint data merged
	var overallP50, overallP95, overallP99 float64
	if len(endpoints) > 0 {
		// Weighted average of percentiles (approximation)
		var weightedP50, weightedP95, weightedP99 float64
		for _, ep := range endpoints {
			w := ep.Requests
			weightedP50 += ep.P50Ms * w
			weightedP95 += ep.P95Ms * w
			weightedP99 += ep.P99Ms * w
		}
		if totalRequests > 0 {
			overallP50 = weightedP50 / totalRequests
			overallP95 = weightedP95 / totalRequests
			overallP99 = weightedP99 / totalRequests
		}
	}

	// Build endpoint list with error rate per endpoint
	type endpointInfo struct {
		Method    string  `json:"method"`
		Path      string  `json:"path"`
		Requests  float64 `json:"requests"`
		AvgMs     float64 `json:"avgLatencyMs"`
		P50Ms     float64 `json:"p50LatencyMs"`
		P95Ms     float64 `json:"p95LatencyMs"`
		P99Ms     float64 `json:"p99LatencyMs"`
		ErrorRate float64 `json:"errorRate"`
	}

	epList := make([]endpointInfo, 0, len(endpoints))
	for _, ep := range endpoints {
		er := 0.0
		if ep.Requests > 0 {
			er = ep.Errors / ep.Requests
		}
		epList = append(epList, endpointInfo{
			Method:    ep.Method,
			Path:      ep.Path,
			Requests:  ep.Requests,
			AvgMs:     ep.AvgMs,
			P50Ms:     ep.P50Ms,
			P95Ms:     ep.P95Ms,
			P99Ms:     ep.P99Ms,
			ErrorRate: er,
		})
	}

	// Top 5 slowest endpoints (by p95 latency)
	slowest := make([]endpointInfo, len(epList))
	copy(slowest, epList)
	sort.Slice(slowest, func(i, j int) bool {
		return slowest[i].P95Ms > slowest[j].P95Ms
	})
	if len(slowest) > 5 {
		slowest = slowest[:5]
	}

	// Top 5 highest error rate endpoints (with at least 10 requests)
	highestError := make([]endpointInfo, 0)
	for _, ep := range epList {
		if ep.Requests >= 10 {
			highestError = append(highestError, ep)
		}
	}
	sort.Slice(highestError, func(i, j int) bool {
		return highestError[i].ErrorRate > highestError[j].ErrorRate
	})
	if len(highestError) > 5 {
		highestError = highestError[:5]
	}

	response.OK(c, "API performance retrieved", gin.H{
		"summary": gin.H{
			"totalRequests": totalRequests,
			"avgLatencyMs":  avgLatencyMs,
			"p50LatencyMs":  overallP50,
			"p95LatencyMs":  overallP95,
			"p99LatencyMs":  overallP99,
			"errorRate":     errorRate,
		},
		"endpoints":             epList,
		"slowestEndpoints":      slowest,
		"highestErrorEndpoints": highestError,
	})
}

// Package metrics defines the shared Prometheus collectors and the HTTP/Gin
// middleware that records request counts and latencies, plus the /metrics
// handler each service exposes for scraping.
package metrics

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// RequestDuration is the latency histogram backing the API-performance
	// dashboards and the latency alert rules. The "path" label must be the
	// ROUTE PATTERN, never the raw URL — labelling by raw path would mint a new
	// time series per job ID and blow up Prometheus cardinality.
	RequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "Duration of HTTP requests in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path", "status"})

	// JobsProcessed counts every job a worker reached a terminal state on,
	// success or failure — the "status" label separates them. Divide JobsFailed
	// by this for a failure rate; using JobsFailed alone gives no denominator.
	JobsProcessed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "jobs_processed_total",
		Help: "Total number of jobs processed",
	}, []string{"tool_type", "status"})

	// JobsFailed counts permanent job failures only. Transient failures that
	// get NAKed and redelivered are deliberately not counted here, so this
	// tracks work that was actually lost rather than retry churn. Keep the
	// "reason" label to a small closed set for the same cardinality reason as
	// RequestDuration.
	JobsFailed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "jobs_failed_total",
		Help: "Total number of failed jobs",
	}, []string{"tool_type", "reason"})

	// RateLimitFailOpen counts requests that bypassed rate limiting because the
	// limiter's Redis backend was unavailable. Failing open is deliberate (a
	// Redis blip must not take down request serving) but it means throttling is
	// silently OFF, so it must be observable — alert on any sustained rate.
	// Labels: service, and limiter (the specific limiter that failed open).
	RateLimitFailOpen = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rate_limit_fail_open_total",
		Help: "Requests allowed without a rate-limit check because Redis was unavailable.",
	}, []string{"service", "limiter"})

	// JobDuration is end-to-end job latency: queued (dispatch) → terminal
	// event, including retries and DLQ dwell. Observed once per terminal
	// outcome in shared/queue.recordTerminalJobMetric. Buckets span seconds
	// (typical) to 30 minutes (the JetStream AckWait ceiling).
	JobDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "job_duration_seconds",
		Help:    "End-to-end job latency from queued to terminal state.",
		Buckets: []float64{1, 2.5, 5, 10, 30, 60, 120, 300, 600, 1200, 1800},
	}, []string{"tool_type", "status"})
)

// GinMetricsMiddleware records request duration and status for Prometheus.
func GinMetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		duration := time.Since(start).Seconds()
		status := strconv.Itoa(c.Writer.Status())
		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}
		RequestDuration.WithLabelValues(c.Request.Method, path, status).Observe(duration)
	}
}

// MetricsHandler returns a Gin handler that serves Prometheus metrics at /metrics.
func MetricsHandler() gin.HandlerFunc {
	h := promhttp.Handler()
	return func(c *gin.Context) {
		h.ServeHTTP(c.Writer, c.Request)
	}
}

// routeLabelKey is the private context key carrying the low-cardinality route
// label. It exists so the metrics middleware records a route PATTERN rather than
// the raw path — labelling by raw path would mint a series per resource ID and
// exhaust Prometheus.
type routeLabelKey struct{}

// routeLabel is a mutable per-request holder the metrics middleware seeds and a
// downstream handler fills in with a low-cardinality route template.
type routeLabel struct{ value string }

// SetRouteLabel records the low-cardinality route label (e.g. "/api/jobs") for
// the current request so HTTPMetricsMiddleware uses it instead of the raw path.
// Safe to call when no holder is present (no-op). This is how the api-gateway
// avoids exploding Prometheus cardinality with per-resource UUID paths.
func SetRouteLabel(r *http.Request, label string) {
	if rl, ok := r.Context().Value(routeLabelKey{}).(*routeLabel); ok {
		rl.value = label
	}
}

// HTTPMetricsMiddleware records request duration and status for net/http handlers.
// It labels by a downstream-provided route template (see SetRouteLabel), falling
// back to "other" for unmatched paths — never the raw request path, which for the
// gateway carries per-resource UUIDs and would blow up label cardinality.
func HTTPMetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rl := &routeLabel{}
		r = r.WithContext(context.WithValue(r.Context(), routeLabelKey{}, rl))
		start := time.Now()
		sw := &httpStatusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		duration := time.Since(start).Seconds()
		label := rl.value
		if label == "" {
			label = "other"
		}
		RequestDuration.WithLabelValues(r.Method, label, strconv.Itoa(sw.status)).Observe(duration)
	})
}

// HTTPMetricsHandler returns an http.Handler that serves Prometheus metrics.
func HTTPMetricsHandler() http.Handler {
	return promhttp.Handler()
}

// httpStatusWriter wraps http.ResponseWriter to capture the status code for the
// RequestDuration "status" label. It records the code without touching the body,
// so streaming responses are unaffected.
//
// status stays zero when a handler writes a body without calling WriteHeader;
// callers must treat 0 as the implicit 200 that net/http sends.
type httpStatusWriter struct {
	http.ResponseWriter
	status int
}

func (w *httpStatusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

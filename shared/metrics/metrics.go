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
	RequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "Duration of HTTP requests in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path", "status"})

	JobsProcessed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "jobs_processed_total",
		Help: "Total number of jobs processed",
	}, []string{"tool_type", "status"})

	JobsFailed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "jobs_failed_total",
		Help: "Total number of failed jobs",
	}, []string{"tool_type", "reason"})
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

type httpStatusWriter struct {
	http.ResponseWriter
	status int
}

func (w *httpStatusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

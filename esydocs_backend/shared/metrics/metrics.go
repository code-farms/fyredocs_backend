package metrics

import (
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

// HTTPMetricsMiddleware records request duration and status for net/http handlers.
func HTTPMetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &httpStatusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		duration := time.Since(start).Seconds()
		RequestDuration.WithLabelValues(r.Method, r.URL.Path, strconv.Itoa(sw.status)).Observe(duration)
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

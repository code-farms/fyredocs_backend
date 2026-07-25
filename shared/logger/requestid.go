package logger

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RequestIDHeader is the HTTP header name for request IDs.
const RequestIDHeader = "X-Request-ID"

type requestIDKey struct{}

// RequestIDFromContext extracts the request ID from context.
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(requestIDKey{}).(string); ok {
		return id
	}
	return ""
}

// GinRequestID is Gin middleware that injects a request ID.
func GinRequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(RequestIDHeader)
		if id == "" {
			id = uuid.NewString()
		}
		c.Set("requestID", id)
		c.Request = c.Request.WithContext(
			context.WithValue(c.Request.Context(), requestIDKey{}, id),
		)
		c.Header(RequestIDHeader, id)
		c.Next()
	}
}

// isProbePath reports whether a path is an infrastructure probe (Prometheus
// scrapes every 15s, Docker healthchecks every 5-10s). Logging those at info
// level buries real traffic and dominates Loki ingest, so they are logged only
// when they fail.
func isProbePath(path string) bool {
	switch path {
	case "/metrics", "/healthz", "/readyz":
		return true
	default:
		return false
	}
}

// GinRequestLogger is Gin middleware that logs each request with the request ID.
// Successful probe requests (/metrics, /healthz, /readyz) are skipped; a failing
// probe is still logged, at warn level.
func GinRequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		status := c.Writer.Status()
		if isProbePath(c.Request.URL.Path) {
			if status >= 400 {
				slog.WarnContext(c.Request.Context(), "probe request failed",
					"method", c.Request.Method,
					"path", c.Request.URL.Path,
					"status", status,
					"duration", time.Since(start).String(),
				)
			}
			return
		}

		// *Context so the shared contextHandler attaches trace_id/span_id/request_id.
		slog.InfoContext(c.Request.Context(), "request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", status,
			"duration", time.Since(start).String(),
		)
	}
}

// HTTPRequestID is net/http middleware for the api-gateway.
func HTTPRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if id == "" {
			id = uuid.NewString()
		}
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

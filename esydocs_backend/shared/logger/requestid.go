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

// GinRequestLogger is Gin middleware that logs each request with the request ID.
func GinRequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		reqID, _ := c.Get("requestID")

		c.Next()

		slog.Info("request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration", time.Since(start).String(),
			"requestId", reqID,
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

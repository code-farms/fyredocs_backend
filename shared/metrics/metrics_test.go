package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHTTPMetricsHandler(t *testing.T) {
	handler := HTTPMetricsHandler()
	if handler == nil {
		t.Fatal("expected non-nil handler")
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
}

func TestHTTPMetricsMiddleware(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := HTTPMetricsMiddleware(next)
	if handler == nil {
		t.Fatal("expected non-nil handler")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("expected next handler to be called")
	}
}

func TestGinMetricsMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := GinMetricsMiddleware()
	if handler == nil {
		t.Fatal("expected non-nil handler")
	}
}

func TestMetricsHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := MetricsHandler()
	if handler == nil {
		t.Fatal("expected non-nil handler")
	}
}

func TestHTTPStatusWriterCapturesStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &httpStatusWriter{ResponseWriter: rec, status: http.StatusOK}

	sw.WriteHeader(http.StatusNotFound)

	if sw.status != http.StatusNotFound {
		t.Errorf("expected status %d, got %d", http.StatusNotFound, sw.status)
	}
}

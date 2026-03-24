package telemetry

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInitReturnsShutdownFunc(t *testing.T) {
	shutdown := Init("test-service")
	if shutdown == nil {
		t.Fatal("expected non-nil shutdown function")
	}
	// Call shutdown - should not panic
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown returned error: %v", err)
	}
}

func TestHTTPTraceMiddleware(t *testing.T) {
	wrapper := HTTPTraceMiddleware("test-service")
	if wrapper == nil {
		t.Fatal("expected non-nil middleware wrapper")
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := wrapper(next)
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("expected next handler to be called")
	}
}

func TestStatusWriterCapturesStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec, status: http.StatusOK}

	sw.WriteHeader(http.StatusInternalServerError)

	if sw.status != http.StatusInternalServerError {
		t.Errorf("expected status %d, got %d", http.StatusInternalServerError, sw.status)
	}
}

func TestStatusWriterCapturesSize(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec, status: http.StatusOK}

	n, err := sw.Write([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("expected 5 bytes written, got %d", n)
	}
	if sw.size != 5 {
		t.Errorf("expected size 5, got %d", sw.size)
	}
}

func TestProbeEndpointReachable(t *testing.T) {
	// Start a local TCP listener to simulate a reachable collector.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	endpoint := "http://" + ln.Addr().String()
	if !probeEndpoint(endpoint) {
		t.Error("expected probeEndpoint to return true for reachable listener")
	}
}

func TestProbeEndpointUnreachable(t *testing.T) {
	// Use a port that is almost certainly not listening.
	if probeEndpoint("http://127.0.0.1:1") {
		t.Error("expected probeEndpoint to return false for unreachable host")
	}
}

func TestProbeEndpointInvalidURL(t *testing.T) {
	if probeEndpoint("://bad-url") {
		t.Error("expected probeEndpoint to return false for invalid URL")
	}
}

func TestInitDisabledWhenUnreachable(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	shutdown := Init("test-unreachable")
	if shutdown == nil {
		t.Fatal("expected non-nil shutdown function")
	}
	// Tracer should still be usable (noop) even when collector is unreachable.
	if Tracer == nil {
		t.Error("expected Tracer to be set even when collector is unreachable")
	}
}

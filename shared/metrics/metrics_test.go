package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// pathLabelsObserved returns the set of "path" label values recorded on the
// RequestDuration histogram (across the whole process).
func pathLabelsObserved(t *testing.T) map[string]bool {
	t.Helper()
	got := map[string]bool{}
	ch := make(chan prometheus.Metric, 256)
	RequestDuration.Collect(ch)
	close(ch)
	for m := range ch {
		var d dto.Metric
		if err := m.Write(&d); err != nil {
			continue
		}
		for _, l := range d.GetLabel() {
			if l.GetName() == "path" {
				got[l.GetValue()] = true
			}
		}
	}
	return got
}

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

// TestHTTPMetricsMiddlewareUsesRouteLabel is the C3 regression guard: the HTTP
// middleware must label by the low-cardinality route template set via
// SetRouteLabel, NOT the raw request path (which carries per-resource UUIDs and
// would explode Prometheus series cardinality). Unmatched requests fall back to
// "other".
func TestHTTPMetricsMiddlewareUsesRouteLabel(t *testing.T) {
	// Matched route: handler sets the template label.
	matched := HTTPMetricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetRouteLabel(r, "/api/jobs")
		w.WriteHeader(http.StatusOK)
	}))
	matched.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/api/jobs/0193abc-uuid-here", nil))

	// Unmatched route: handler sets nothing → "other".
	unmatched := HTTPMetricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	unmatched.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/some/unmatched/1a2b3c", nil))

	labels := pathLabelsObserved(t)
	if !labels["/api/jobs"] {
		t.Errorf("expected route-template label %q to be recorded, got labels: %v", "/api/jobs", labels)
	}
	if !labels["other"] {
		t.Errorf("expected fallback label %q for unmatched route, got labels: %v", "other", labels)
	}
	if labels["/api/jobs/0193abc-uuid-here"] {
		t.Error("raw UUID path was recorded as a label — cardinality bomb not fixed")
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

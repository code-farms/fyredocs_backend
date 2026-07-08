package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func doQueuesRequest(t *testing.T) map[string]interface{} {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/admin/metrics/queues", QueueStatus)

	req := httptest.NewRequest(http.MethodGet, "/admin/metrics/queues", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body struct {
		Success bool                   `json:"success"`
		Data    map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !body.Success {
		t.Errorf("success = false, want true")
	}
	return body.Data
}

func TestQueueStatus_Shape(t *testing.T) {
	fakeNATSMonitor(t) // reuses the JOBS_DLQ + JOBS_DISPATCH fixture from nats_test.go
	data := doQueuesRequest(t)

	// Streams are name-sorted; the fixture has JOBS_DISPATCH and JOBS_DLQ.
	streams, _ := data["streams"].([]interface{})
	if len(streams) != 2 {
		t.Fatalf("streams = %d, want 2", len(streams))
	}

	dispatch, _ := data["dispatchConsumers"].([]interface{})
	if len(dispatch) != 1 {
		t.Fatalf("dispatchConsumers = %d, want 1", len(dispatch))
	}
	worker := dispatch[0].(map[string]interface{})
	if worker["pending"].(float64) != 3 || worker["redelivered"].(float64) != 1 {
		t.Errorf("dispatch consumer = %+v, want pending 3 / redelivered 1", worker)
	}

	dlq, _ := data["dlq"].(map[string]interface{})
	if dlq["messages"].(float64) != 7 {
		t.Errorf("dlq.messages = %v, want 7", dlq["messages"])
	}

	// analyticsLag is always present (zeroed when those consumers are absent).
	lag, _ := data["analyticsLag"].(map[string]interface{})
	if _, ok := lag["analytics"].(map[string]interface{}); !ok {
		t.Errorf("analyticsLag.analytics missing: %+v", lag)
	}

	// throughput must be a (possibly empty) array, never null, so the chart maps.
	if _, ok := data["throughput"].([]interface{}); !ok {
		t.Errorf("throughput = %v, want array", data["throughput"])
	}
}

func TestQueueStatus_Unreachable(t *testing.T) {
	t.Setenv("NATS_MONITOR_URL", "http://127.0.0.1:1")
	data := doQueuesRequest(t)

	// NATS down → empty snapshot sections (not null, not 5xx).
	if streams, ok := data["streams"].([]interface{}); !ok || len(streams) != 0 {
		t.Errorf("streams = %v, want empty array", data["streams"])
	}
	if _, ok := data["throughput"].([]interface{}); !ok {
		t.Errorf("throughput = %v, want array even when NATS is down", data["throughput"])
	}
	dlq, _ := data["dlq"].(map[string]interface{})
	if dlq["messages"].(float64) != 0 {
		t.Errorf("dlq.messages = %v, want 0 when NATS unreachable", dlq["messages"])
	}
}

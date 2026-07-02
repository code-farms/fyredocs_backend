package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

const natsVarz = `{"server_id":"NX","version":"2.10.0","connections":3,"total_connections":30,"mem":10485760,"cpu":0.5,"slow_consumers":0,"uptime":"5m"}`

const natsJsz = `{
  "streams": 2, "consumers": 1, "messages": 12, "bytes": 2048,
  "account_details": [{
    "name": "$G",
    "stream_detail": [
      {"name":"JOBS_DLQ","state":{"messages":7,"bytes":700,"first_seq":1,"last_seq":7,"consumer_count":0},"consumer_detail":[]},
      {"name":"JOBS_DISPATCH","state":{"messages":5,"bytes":500,"first_seq":1,"last_seq":5,"consumer_count":1},
        "consumer_detail":[{"stream_name":"JOBS_DISPATCH","name":"worker","num_ack_pending":2,"num_redelivered":1,"num_pending":3,"num_waiting":0}]}
    ]
  }]
}`

func fakeNATSMonitor(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/varz":
			_, _ = w.Write([]byte(natsVarz))
		case "/jsz":
			_, _ = w.Write([]byte(natsJsz))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("NATS_MONITOR_URL", srv.URL)
}

func doNATSRequest(t *testing.T) map[string]interface{} {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/admin/metrics/nats", NATSStats)

	req := httptest.NewRequest(http.MethodGet, "/admin/metrics/nats", nil)
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

func TestNATSStats_Shape(t *testing.T) {
	fakeNATSMonitor(t)
	data := doNATSRequest(t)

	server, _ := data["server"].(map[string]interface{})
	if server["status"] != "healthy" {
		t.Errorf("server status = %v, want healthy", server["status"])
	}
	if server["connections"].(float64) != 3 {
		t.Errorf("connections = %v, want 3", server["connections"])
	}

	streams, _ := data["streams"].([]interface{})
	if len(streams) != 2 {
		t.Fatalf("streams = %d, want 2", len(streams))
	}
	// Streams are name-sorted: JOBS_DISPATCH before JOBS_DLQ.
	first := streams[0].(map[string]interface{})
	if first["name"] != "JOBS_DISPATCH" {
		t.Errorf("first stream = %v, want JOBS_DISPATCH", first["name"])
	}

	consumers, _ := data["consumers"].([]interface{})
	if len(consumers) != 1 {
		t.Fatalf("consumers = %d, want 1", len(consumers))
	}

	summary, _ := data["summary"].(map[string]interface{})
	if summary["dlqDepth"].(float64) != 7 {
		t.Errorf("dlqDepth = %v, want 7", summary["dlqDepth"])
	}
	if summary["totalMessages"].(float64) != 12 {
		t.Errorf("totalMessages = %v, want 12", summary["totalMessages"])
	}
	if summary["totalStreams"].(float64) != 2 {
		t.Errorf("totalStreams = %v, want 2", summary["totalStreams"])
	}
}

func TestNATSStats_Unreachable(t *testing.T) {
	t.Setenv("NATS_MONITOR_URL", "http://127.0.0.1:1")
	data := doNATSRequest(t)

	server, _ := data["server"].(map[string]interface{})
	if server["status"] != "unreachable" {
		t.Errorf("server status = %v, want unreachable", server["status"])
	}
	summary, _ := data["summary"].(map[string]interface{})
	if summary["status"] != "unreachable" {
		t.Errorf("summary status = %v, want unreachable", summary["status"])
	}
	// Streams/consumers must be empty arrays, not null, so the UI can map over them.
	if streams, ok := data["streams"].([]interface{}); !ok || len(streams) != 0 {
		t.Errorf("streams = %v, want empty array", data["streams"])
	}
}

func TestNATSMonitorURL_Default(t *testing.T) {
	t.Setenv("NATS_MONITOR_URL", "")
	if got := natsMonitorURL(); got != "http://nats:8222" {
		t.Errorf("natsMonitorURL() = %q, want http://nats:8222", got)
	}
}

func TestNATSMonitorURL_TrimsTrailingSlash(t *testing.T) {
	t.Setenv("NATS_MONITOR_URL", "http://nats:8222/")
	if got := natsMonitorURL(); got != "http://nats:8222" {
		t.Errorf("natsMonitorURL() = %q, want http://nats:8222", got)
	}
}

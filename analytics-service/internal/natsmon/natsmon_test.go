package natsmon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const jszBody = `{
  "streams": 2,
  "consumers": 1,
  "messages": 12,
  "bytes": 2048,
  "account_details": [
    {
      "name": "$G",
      "stream_detail": [
        {
          "name": "JOBS_DISPATCH",
          "state": {"messages": 5, "bytes": 1024, "first_seq": 1, "last_seq": 5, "consumer_count": 1},
          "consumer_detail": [
            {"stream_name": "JOBS_DISPATCH", "name": "worker", "num_ack_pending": 2, "num_redelivered": 1, "num_pending": 3, "num_waiting": 0}
          ]
        },
        {
          "name": "JOBS_DLQ",
          "state": {"messages": 7, "bytes": 1024, "first_seq": 1, "last_seq": 7, "consumer_count": 0},
          "consumer_detail": []
        }
      ]
    }
  ]
}`

const varzBody = `{
  "server_id": "NABC",
  "version": "2.10.0",
  "connections": 4,
  "total_connections": 40,
  "mem": 10485760,
  "cpu": 1.5,
  "slow_consumers": 0,
  "uptime": "1h2m3s"
}`

func newMonitor(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/varz":
			_, _ = w.Write([]byte(varzBody))
		case "/jsz":
			_, _ = w.Write([]byte(jszBody))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestServerInfo(t *testing.T) {
	v, err := ServerInfo(context.Background(), newMonitor(t))
	if err != nil {
		t.Fatalf("ServerInfo error: %v", err)
	}
	if v.Connections != 4 {
		t.Errorf("connections = %d, want 4", v.Connections)
	}
	if v.Mem != 10485760 {
		t.Errorf("mem = %d, want 10485760", v.Mem)
	}
	if v.Uptime != "1h2m3s" {
		t.Errorf("uptime = %q, want 1h2m3s", v.Uptime)
	}
}

func TestJetStreamInfo(t *testing.T) {
	j, err := JetStreamInfo(context.Background(), newMonitor(t))
	if err != nil {
		t.Fatalf("JetStreamInfo error: %v", err)
	}
	if len(j.AccountDetails) != 1 {
		t.Fatalf("account_details = %d, want 1", len(j.AccountDetails))
	}
	streams := j.AccountDetails[0].StreamDetail
	if len(streams) != 2 {
		t.Fatalf("streams = %d, want 2", len(streams))
	}
	if streams[0].Name != "JOBS_DISPATCH" || streams[0].State.Messages != 5 {
		t.Errorf("unexpected first stream: %+v", streams[0])
	}
	if len(streams[0].ConsumerDetail) != 1 || streams[0].ConsumerDetail[0].NumPending != 3 {
		t.Errorf("unexpected consumer detail: %+v", streams[0].ConsumerDetail)
	}
}

func TestFetchJSON_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := ServerInfo(context.Background(), srv.URL); err == nil {
		t.Error("expected error on non-200 response, got nil")
	}
}

func TestFetchJSON_ConnRefused(t *testing.T) {
	// Port 1 is reserved/unused — connection should be refused quickly.
	if _, err := JetStreamInfo(context.Background(), "http://127.0.0.1:1"); err == nil {
		t.Error("expected error on unreachable server, got nil")
	}
}

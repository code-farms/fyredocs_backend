// Package natsmon fetches and parses data from the NATS server's HTTP
// monitoring endpoint (enabled via `-m 8222`). It mirrors the internal
// promscrape package: a small, testable HTTP-fetch-and-decode helper that the
// analytics-service uses to surface NATS/JetStream health on the admin
// dashboard. NATS is internal-only on the compose network, so the monitoring
// port is reachable service-to-service but never exposed to the internet.
package natsmon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// FetchTimeout is the default timeout for a single monitoring request.
const FetchTimeout = 5 * time.Second

// Varz is the subset of the NATS /varz server-info response we surface.
type Varz struct {
	ServerID      string  `json:"server_id"`
	Version       string  `json:"version"`
	Connections   int     `json:"connections"`
	TotalConns    int64   `json:"total_connections"`
	Mem           int64   `json:"mem"`
	CPU           float64 `json:"cpu"`
	SlowConsumers int64   `json:"slow_consumers"`
	Uptime        string  `json:"uptime"`
}

// JSZ is the subset of the NATS /jsz JetStream response we surface. It is
// fetched with streams=true&consumers=true so stream and consumer detail are
// nested under account_details.
type JSZ struct {
	Streams        int             `json:"streams"`
	Consumers      int             `json:"consumers"`
	Messages       uint64          `json:"messages"`
	Bytes          uint64          `json:"bytes"`
	AccountDetails []AccountDetail `json:"account_details"`
}

// AccountDetail groups stream detail per JetStream account.
type AccountDetail struct {
	Name         string         `json:"name"`
	StreamDetail []StreamDetail `json:"stream_detail"`
}

// StreamDetail describes a single JetStream stream and its consumers.
type StreamDetail struct {
	Name           string           `json:"name"`
	State          StreamState      `json:"state"`
	ConsumerDetail []ConsumerDetail `json:"consumer_detail"`
}

// StreamState holds the message/byte counters for a stream.
type StreamState struct {
	Messages      uint64 `json:"messages"`
	Bytes         uint64 `json:"bytes"`
	FirstSeq      uint64 `json:"first_seq"`
	LastSeq       uint64 `json:"last_seq"`
	ConsumerCount int    `json:"consumer_count"`
}

// ConsumerDetail holds the lag/backlog counters for a single consumer.
type ConsumerDetail struct {
	StreamName     string `json:"stream_name"`
	Name           string `json:"name"`
	NumAckPending  int    `json:"num_ack_pending"`
	NumRedelivered int    `json:"num_redelivered"`
	NumPending     uint64 `json:"num_pending"`
	NumWaiting     int    `json:"num_waiting"`
}

// ServerInfo fetches and decodes the NATS /varz server-info endpoint.
func ServerInfo(ctx context.Context, baseURL string) (*Varz, error) {
	var v Varz
	if err := fetchJSON(ctx, baseURL+"/varz", &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// JetStreamInfo fetches and decodes the NATS /jsz endpoint including per-stream
// and per-consumer detail.
func JetStreamInfo(ctx context.Context, baseURL string) (*JSZ, error) {
	var j JSZ
	if err := fetchJSON(ctx, baseURL+"/jsz?streams=true&consumers=true", &j); err != nil {
		return nil, err
	}
	return &j, nil
}

// fetchJSON GETs url with a bounded timeout and decodes the JSON body into out.
func fetchJSON(ctx context.Context, url string, out interface{}) error {
	reqCtx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetching %s: %w", url, err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetching %s: status %d", url, resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding %s: %w", url, err)
	}
	return nil
}

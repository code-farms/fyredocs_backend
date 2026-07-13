// Package natsconn manages the shared NATS/JetStream connection and stream
// setup used for cross-service events and job dispatch. It exposes the core
// connection and the JetStream context as package globals initialized by Connect.
package natsconn

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

var (
	Conn *nats.Conn
	JS   jetstream.JetStream
)

// Connect establishes a connection to the NATS server and initializes JetStream.
// It reads the NATS_URL environment variable (default: "nats://nats:4222").
func Connect() error {
	url := os.Getenv("NATS_URL")
	if url == "" {
		url = nats.DefaultURL
	}

	nc, err := nats.Connect(url,
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				slog.Warn("NATS disconnected", "error", err)
			}
		}),
		nats.ReconnectHandler(func(_ *nats.Conn) {
			slog.Info("NATS reconnected")
		}),
	)
	if err != nil {
		return fmt.Errorf("nats connect failed: %w", err)
	}

	Conn = nc
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return fmt.Errorf("jetstream init failed: %w", err)
	}
	JS = js

	slog.Info("NATS JetStream connection established", "url", url)
	return nil
}

// streamConfigs returns the canonical JetStream stream definitions for the
// pipeline. Job payloads carry object-storage keys (not file bytes), so
// per-message sizes are small; MaxBytes/MaxMsgSize cap disk usage and reject
// accidentally oversized payloads instead of letting a bug fill the volume.
func streamConfigs() []jetstream.StreamConfig {
	return []jetstream.StreamConfig{
		{
			// JOBS_DISPATCH: WorkQueue retention ensures each message is consumed exactly once.
			Name:       "JOBS_DISPATCH",
			Subjects:   []string{"jobs.dispatch.>"},
			Storage:    jetstream.FileStorage,
			Retention:  jetstream.WorkQueuePolicy,
			MaxAge:     24 * time.Hour,
			MaxBytes:   1 << 30,  // 1 GiB stream cap
			MaxMsgSize: 64 << 10, // 64 KiB — payloads are object keys + metadata, never file bytes
		},
		{
			// JOBS_EVENTS: Interest retention keeps messages while consumers need them.
			Name:      "JOBS_EVENTS",
			Subjects:  []string{"jobs.events.>"},
			Storage:   jetstream.FileStorage,
			Retention: jetstream.InterestPolicy,
			MaxAge:    1 * time.Hour,
			MaxBytes:  256 << 20, // 256 MiB
		},
		{
			// JOBS_DLQ: Captures permanently failed messages for investigation.
			Name:      "JOBS_DLQ",
			Subjects:  []string{"jobs.dlq.>"},
			Storage:   jetstream.FileStorage,
			Retention: jetstream.LimitsPolicy,
			MaxAge:    7 * 24 * time.Hour, // keep failed messages for 7 days
			MaxBytes:  256 << 20,          // 256 MiB
		},
		{
			// ANALYTICS: Captures analytics events for business metrics.
			Name:      "ANALYTICS",
			Subjects:  []string{"analytics.events.>"},
			Storage:   jetstream.FileStorage,
			Retention: jetstream.InterestPolicy,
			MaxAge:    24 * time.Hour,
			MaxBytes:  256 << 20, // 256 MiB
		},
	}
}

// EnsureStreams creates or updates the JetStream streams used by the pipeline.
// Safe to call multiple times (idempotent).
func EnsureStreams(ctx context.Context) error {
	if JS == nil {
		return fmt.Errorf("jetstream not initialized")
	}

	names := make([]string, 0, 4)
	for _, cfg := range streamConfigs() {
		if _, err := JS.CreateOrUpdateStream(ctx, cfg); err != nil {
			return fmt.Errorf("create %s stream: %w", cfg.Name, err)
		}
		names = append(names, cfg.Name)
	}

	slog.Info("NATS JetStream streams ensured", "streams", names)
	return nil
}

// Close gracefully closes the NATS connection.
func Close() {
	if Conn != nil {
		Conn.Drain()
		Conn.Close()
	}
}

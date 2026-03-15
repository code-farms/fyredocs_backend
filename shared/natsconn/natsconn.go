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

// EnsureStreams creates or updates the JetStream streams used by the pipeline.
// Safe to call multiple times (idempotent).
func EnsureStreams(ctx context.Context) error {
	if JS == nil {
		return fmt.Errorf("jetstream not initialized")
	}

	// JOBS_DISPATCH: WorkQueue retention ensures each message is consumed exactly once.
	_, err := JS.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      "JOBS_DISPATCH",
		Subjects:  []string{"jobs.dispatch.>"},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.WorkQueuePolicy,
		MaxAge:    24 * time.Hour,
	})
	if err != nil {
		return fmt.Errorf("create JOBS_DISPATCH stream: %w", err)
	}

	// JOBS_EVENTS: Interest retention keeps messages while consumers need them.
	_, err = JS.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      "JOBS_EVENTS",
		Subjects:  []string{"jobs.events.>"},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.InterestPolicy,
		MaxAge:    1 * time.Hour,
	})
	if err != nil {
		return fmt.Errorf("create JOBS_EVENTS stream: %w", err)
	}

	slog.Info("NATS JetStream streams ensured", "streams", []string{"JOBS_DISPATCH", "JOBS_EVENTS"})
	return nil
}

// Close gracefully closes the NATS connection.
func Close() {
	if Conn != nil {
		Conn.Drain()
		Conn.Close()
	}
}

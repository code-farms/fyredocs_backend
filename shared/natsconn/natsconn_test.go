package natsconn

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

func TestEnsureStreamsRequiresInitializedJetStream(t *testing.T) {
	old := JS
	JS = nil
	t.Cleanup(func() { JS = old })

	if err := EnsureStreams(context.Background()); err == nil {
		t.Fatal("expected error when JetStream is not initialized")
	}
}

func TestStreamConfigs(t *testing.T) {
	configs := streamConfigs()

	byName := make(map[string]jetstream.StreamConfig, len(configs))
	for _, cfg := range configs {
		byName[cfg.Name] = cfg
	}

	tests := []struct {
		name       string
		subjects   []string
		retention  jetstream.RetentionPolicy
		maxAge     time.Duration
		maxBytes   int64
		maxMsgSize int32
	}{
		{
			name:       "JOBS_DISPATCH",
			subjects:   []string{"jobs.dispatch.>"},
			retention:  jetstream.WorkQueuePolicy,
			maxAge:     24 * time.Hour,
			maxBytes:   1 << 30,
			maxMsgSize: 64 << 10,
		},
		{
			name:      "JOBS_EVENTS",
			subjects:  []string{"jobs.events.>"},
			retention: jetstream.InterestPolicy,
			maxAge:    1 * time.Hour,
			maxBytes:  256 << 20,
		},
		{
			name:      "JOBS_DLQ",
			subjects:  []string{"jobs.dlq.>"},
			retention: jetstream.LimitsPolicy,
			maxAge:    7 * 24 * time.Hour,
			maxBytes:  256 << 20,
		},
		{
			name:      "ANALYTICS",
			subjects:  []string{"analytics.events.>"},
			retention: jetstream.InterestPolicy,
			maxAge:    24 * time.Hour,
			maxBytes:  256 << 20,
		},
	}

	if len(configs) != len(tests) {
		t.Fatalf("expected %d streams, got %d", len(tests), len(configs))
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, ok := byName[tt.name]
			if !ok {
				t.Fatalf("stream %s not defined", tt.name)
			}
			if len(cfg.Subjects) != 1 || cfg.Subjects[0] != tt.subjects[0] {
				t.Errorf("subjects = %v, want %v", cfg.Subjects, tt.subjects)
			}
			if cfg.Retention != tt.retention {
				t.Errorf("retention = %v, want %v", cfg.Retention, tt.retention)
			}
			if cfg.MaxAge != tt.maxAge {
				t.Errorf("maxAge = %v, want %v", cfg.MaxAge, tt.maxAge)
			}
			if cfg.MaxBytes != tt.maxBytes {
				t.Errorf("maxBytes = %d, want %d", cfg.MaxBytes, tt.maxBytes)
			}
			if cfg.MaxMsgSize != tt.maxMsgSize {
				t.Errorf("maxMsgSize = %d, want %d", cfg.MaxMsgSize, tt.maxMsgSize)
			}
			if cfg.Storage != jetstream.FileStorage {
				t.Errorf("storage = %v, want FileStorage", cfg.Storage)
			}
		})
	}
}

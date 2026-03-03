package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/redis/go-redis/v9"
)

type JobPayload struct {
	JobID         string          `json:"jobId"`
	ToolType      string          `json:"toolType"`
	InputPaths    []string        `json:"inputPaths"`
	Options       json.RawMessage `json:"options,omitempty"`
	Attempts      int             `json:"attempts"`
	CorrelationID string          `json:"correlationId"`
}

func Enqueue(ctx context.Context, client *redis.Client, queueName string, payload JobPayload) error {
	if client == nil {
		return fmt.Errorf("redis client is not initialized")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return client.LPush(ctx, queueName, data).Err()
}

func QueueNameForWorker(worker string) string {
	prefix := os.Getenv("QUEUE_PREFIX")
	if prefix == "" {
		prefix = "queue"
	}
	return fmt.Sprintf("%s:%s", prefix, worker)
}

package queue

import (
	"context"
	"os"
	"testing"
)

func TestEnqueueNilClient(t *testing.T) {
	err := Enqueue(context.Background(), nil, "test-queue", JobPayload{JobID: "test"})
	if err == nil {
		t.Error("expected error when redis client is nil")
	}
}

func TestQueueNameForWorker(t *testing.T) {
	os.Setenv("QUEUE_PREFIX", "myqueue")
	defer os.Unsetenv("QUEUE_PREFIX")

	name := QueueNameForWorker("convert-from-pdf")
	expected := "myqueue:convert-from-pdf"
	if name != expected {
		t.Errorf("QueueNameForWorker = %q, want %q", name, expected)
	}
}

func TestQueueNameForWorkerDefault(t *testing.T) {
	os.Unsetenv("QUEUE_PREFIX")
	name := QueueNameForWorker("test-service")
	expected := "queue:test-service"
	if name != expected {
		t.Errorf("QueueNameForWorker = %q, want %q", name, expected)
	}
}

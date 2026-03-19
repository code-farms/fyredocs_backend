package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nats-io/nats.go/jetstream"

	"esydocs/shared/natsconn"
	"esydocs/shared/queue"
)

// SSEJobUpdates streams real-time job status updates via Server-Sent Events.
// Clients connect to GET /api/jobs/:id/events to receive updates for a specific job.
func SSEJobUpdates(c *gin.Context) {
	jobID := c.Param("id")
	if jobID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "job ID required"})
		return
	}

	// Set SSE headers
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering
	c.Writer.Flush()

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()

	if natsconn.JS == nil {
		fmt.Fprintf(c.Writer, "event: error\ndata: {\"message\":\"event stream unavailable\"}\n\n")
		c.Writer.Flush()
		return
	}

	// Create an ephemeral consumer for this SSE connection
	cons, err := natsconn.JS.CreateConsumer(ctx, "JOBS_EVENTS", jetstream.ConsumerConfig{
		FilterSubject:     "jobs.events.>",
		DeliverPolicy:     jetstream.DeliverNewPolicy,
		AckPolicy:         jetstream.AckExplicitPolicy,
		InactiveThreshold: 1 * time.Minute,
	})
	if err != nil {
		slog.Error("SSE: failed to create NATS consumer", "jobId", jobID, "error", err)
		fmt.Fprintf(c.Writer, "event: error\ndata: {\"message\":\"failed to subscribe\"}\n\n")
		c.Writer.Flush()
		return
	}

	// Send initial connected event
	fmt.Fprintf(c.Writer, "event: connected\ndata: {\"jobId\":\"%s\"}\n\n", jobID)
	c.Writer.Flush()

	// Send a keepalive comment every 15 seconds to prevent connection timeout
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-keepalive.C:
			fmt.Fprintf(c.Writer, ": keepalive\n\n")
			c.Writer.Flush()
		default:
			msgs, err := cons.Fetch(1, jetstream.FetchMaxWait(5*time.Second))
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				continue
			}

			for msg := range msgs.Messages() {
				var event queue.JobEvent
				if err := json.Unmarshal(msg.Data(), &event); err != nil {
					_ = msg.Ack()
					continue
				}

				// Only forward events for the requested job
				if event.JobID != jobID {
					_ = msg.Ack()
					continue
				}

				data, _ := json.Marshal(gin.H{
					"jobId":    event.JobID,
					"status":   event.EventType,
					"progress": event.Progress,
					"toolType": event.ToolType,
				})
				fmt.Fprintf(c.Writer, "event: job-update\ndata: %s\n\n", data)
				c.Writer.Flush()
				_ = msg.Ack()

				// If job completed or failed, close the stream
				if event.EventType == "JobCompleted" || event.EventType == "JobFailed" {
					fmt.Fprintf(c.Writer, "event: done\ndata: {\"jobId\":\"%s\"}\n\n", jobID)
					c.Writer.Flush()
					return
				}
			}
		}
	}
}

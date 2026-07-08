package handlers

import (
	"sort"
	"time"

	"github.com/gin-gonic/gin"

	"analytics-service/internal/models"
	"analytics-service/internal/natsmon"
	"fyredocs/shared/response"
)

// Stream names introspected for queue health (mirror shared/natsconn setup).
const (
	dispatchStreamName   = "JOBS_DISPATCH"
	analyticsStreamName  = "ANALYTICS"
	jobsEventsStreamName = "JOBS_EVENTS"
)

// QueueStatus returns queue health for the admin System page: NATS stream depth,
// dispatch-consumer backlog, DLQ depth, analytics/job-event consumer lag, plus a
// DB-derived event-throughput timeseries. Like NATSStats it degrades gracefully —
// an unreachable NATS server yields empty snapshot sections (not a 5xx), and the
// DB-derived throughput still renders.
func QueueStatus(c *gin.Context) {
	jsz, jszErr := natsmon.JetStreamInfo(c.Request.Context(), natsMonitorURL())
	streams, dispatchConsumers, dlq, analyticsLag := buildQueueStatus(jsz, jszErr)

	response.OK(c, "Queue status retrieved", gin.H{
		"timestamp":         time.Now().UTC().Format(time.RFC3339),
		"streams":           streams,
		"dispatchConsumers": dispatchConsumers,
		"dlq":               dlq,
		"analyticsLag":      analyticsLag,
		"throughput":        queueThroughput(time.Now().UTC()),
	})
}

// buildQueueStatus flattens the /jsz payload into the queue-status snapshot. On
// error/nil it returns empty (non-nil) sections so the UI can render.
func buildQueueStatus(j *natsmon.JSZ, err error) (streams []gin.H, dispatchConsumers []gin.H, dlq gin.H, analyticsLag gin.H) {
	streams = []gin.H{}
	dispatchConsumers = []gin.H{}
	dlq = gin.H{"messages": uint64(0), "oldestAgeSeconds": nil}

	var anPending, jePending uint64
	var anAck, jeAck int

	if err == nil && j != nil {
		for _, acct := range j.AccountDetails {
			for _, s := range acct.StreamDetail {
				streams = append(streams, gin.H{
					"name":            s.Name,
					"messages":        s.State.Messages,
					"bytes":           s.State.Bytes,
					"consumers":       s.State.ConsumerCount,
					"oldestMessageAt": nil,
				})
				if s.Name == dlqStreamName {
					dlq["messages"] = s.State.Messages
				}
				for _, cons := range s.ConsumerDetail {
					switch s.Name {
					case dispatchStreamName:
						dispatchConsumers = append(dispatchConsumers, gin.H{
							"name":        cons.Name,
							"pending":     cons.NumPending,
							"ackPending":  cons.NumAckPending,
							"redelivered": cons.NumRedelivered,
						})
					case analyticsStreamName:
						anPending += cons.NumPending
						anAck += cons.NumAckPending
					case jobsEventsStreamName:
						jePending += cons.NumPending
						jeAck += cons.NumAckPending
					}
				}
			}
		}
	}

	sort.Slice(streams, func(a, b int) bool {
		return streams[a]["name"].(string) < streams[b]["name"].(string)
	})
	sort.Slice(dispatchConsumers, func(a, b int) bool {
		return dispatchConsumers[a]["name"].(string) < dispatchConsumers[b]["name"].(string)
	})

	analyticsLag = gin.H{
		"analytics":  gin.H{"pending": anPending, "ackPending": anAck},
		"jobsEvents": gin.H{"pending": jePending, "ackPending": jeAck},
	}
	return streams, dispatchConsumers, dlq, analyticsLag
}

// queueThroughput returns hourly event-pipeline throughput for the last 24h from
// analytics_events. `queued` is always 0: NATS queue depth is only a live
// snapshot (see analyticsLag/dlq), not persisted over time. Returns an empty
// (non-nil) slice when the DB is unavailable.
func queueThroughput(now time.Time) []gin.H {
	out := []gin.H{}
	if models.DB == nil {
		return out
	}

	type throughputRow struct {
		Time      string `json:"time"`
		Processed int64  `json:"processed"`
		Failed    int64  `json:"failed"`
	}
	var rows []throughputRow
	models.DB.Raw(`
		SELECT DATE_TRUNC('hour', created_at) as time,
			COUNT(*) as processed,
			COUNT(*) FILTER (WHERE event_type = 'job.failed') as failed
		FROM analytics_events
		WHERE created_at >= ?
		GROUP BY DATE_TRUNC('hour', created_at)
		ORDER BY time ASC
	`, now.Add(-24*time.Hour)).Scan(&rows)

	for _, r := range rows {
		out = append(out, gin.H{
			"time":      r.Time,
			"processed": r.Processed,
			"failed":    r.Failed,
			"queued":    0,
		})
	}
	return out
}

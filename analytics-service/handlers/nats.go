package handlers

import (
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"analytics-service/internal/natsmon"
	"fyredocs/shared/response"
)

// dlqStreamName is the JetStream stream capturing permanently failed jobs.
// Its depth is surfaced as a top-line summary metric because a growing DLQ is
// the clearest signal that the pipeline is unhealthy.
const dlqStreamName = "JOBS_DLQ"

// NATSStats returns NATS/JetStream health for the admin dashboard: server-level
// info from /varz and per-stream + per-consumer detail from /jsz. The NATS
// monitoring endpoint is internal-only (never host-exposed); the analytics
// service proxies and shapes it. Fetches run in parallel and degrade
// gracefully — an unreachable server yields status "unreachable" rather than a
// 5xx, so the panel can still render.
func NATSStats(c *gin.Context) {
	ctx := c.Request.Context()
	base := natsMonitorURL()

	var (
		wg      sync.WaitGroup
		varz    *natsmon.Varz
		varzErr error
		jsz     *natsmon.JSZ
		jszErr  error
	)

	wg.Add(2)
	go func() {
		defer wg.Done()
		varz, varzErr = natsmon.ServerInfo(ctx, base)
	}()
	go func() {
		defer wg.Done()
		jsz, jszErr = natsmon.JetStreamInfo(ctx, base)
	}()
	wg.Wait()

	server := buildServerInfo(varz, varzErr)
	streams, consumers, summary := buildJetStreamInfo(jsz, jszErr)

	response.OK(c, "NATS metrics retrieved", gin.H{
		"server":    server,
		"streams":   streams,
		"consumers": consumers,
		"summary":   summary,
	})
}

// natsMonitorURL returns the NATS HTTP monitoring base URL, defaulting to the
// compose service address when NATS_MONITOR_URL is unset.
func natsMonitorURL() string {
	if url := strings.TrimSpace(os.Getenv("NATS_MONITOR_URL")); url != "" {
		return strings.TrimRight(url, "/")
	}
	return "http://nats:8222"
}

func buildServerInfo(v *natsmon.Varz, err error) gin.H {
	if err != nil || v == nil {
		info := gin.H{"status": "unreachable"}
		if err != nil {
			info["error"] = err.Error()
		}
		return info
	}
	return gin.H{
		"status":           "healthy",
		"serverId":         v.ServerID,
		"version":          v.Version,
		"connections":      v.Connections,
		"totalConnections": v.TotalConns,
		"memoryMB":         roundMB(uint64(max64(v.Mem, 0))),
		"cpuPercent":       v.CPU,
		"slowConsumers":    v.SlowConsumers,
		"uptime":           v.Uptime,
	}
}

// buildJetStreamInfo flattens the account-nested /jsz payload into a
// table-friendly, name-sorted streams list and consumers list, plus a summary.
func buildJetStreamInfo(j *natsmon.JSZ, err error) (streams []gin.H, consumers []gin.H, summary gin.H) {
	streams = []gin.H{}
	consumers = []gin.H{}

	if err != nil || j == nil {
		summary = gin.H{"status": "unreachable"}
		if err != nil {
			summary["error"] = err.Error()
		}
		return streams, consumers, summary
	}

	var totalMessages uint64
	var dlqDepth uint64

	for _, acct := range j.AccountDetails {
		for _, s := range acct.StreamDetail {
			streams = append(streams, gin.H{
				"name":          s.Name,
				"messages":      s.State.Messages,
				"bytes":         s.State.Bytes,
				"firstSeq":      s.State.FirstSeq,
				"lastSeq":       s.State.LastSeq,
				"consumerCount": s.State.ConsumerCount,
			})
			totalMessages += s.State.Messages
			if s.Name == dlqStreamName {
				dlqDepth = s.State.Messages
			}

			for _, cons := range s.ConsumerDetail {
				consumers = append(consumers, gin.H{
					"stream":         s.Name,
					"name":           cons.Name,
					"numPending":     cons.NumPending,
					"numAckPending":  cons.NumAckPending,
					"numRedelivered": cons.NumRedelivered,
					"numWaiting":     cons.NumWaiting,
				})
			}
		}
	}

	sort.Slice(streams, func(a, b int) bool {
		return streams[a]["name"].(string) < streams[b]["name"].(string)
	})
	sort.Slice(consumers, func(a, b int) bool {
		if consumers[a]["stream"].(string) != consumers[b]["stream"].(string) {
			return consumers[a]["stream"].(string) < consumers[b]["stream"].(string)
		}
		return consumers[a]["name"].(string) < consumers[b]["name"].(string)
	})

	summary = gin.H{
		"status":         "healthy",
		"totalStreams":   len(streams),
		"totalConsumers": len(consumers),
		"totalMessages":  totalMessages,
		"dlqDepth":       dlqDepth,
	}
	return streams, consumers, summary
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

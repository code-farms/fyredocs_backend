// Package opsmetrics runs a background poller in analytics-service that exposes
// operational health as Prometheus gauges: dependency_up{dependency=...} for
// Redis/NATS/Postgres and nats_dlq_pending_messages for the JOBS_DLQ backlog.
// Prometheus scrapes these off analytics-service's /metrics and the alert rules
// (deployment/prometheus/rules) fire on them. Analytics-service is the single
// poller — these are global infra facts, so one prober is enough.
package opsmetrics

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"gorm.io/gorm"

	"fyredocs/shared/config"
	"fyredocs/shared/natsconn"
	"fyredocs/shared/redisstore"
)

const dlqStreamName = "JOBS_DLQ"

var (
	dependencyUp = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dependency_up",
		Help: "Whether a backing dependency is reachable from analytics-service (1=up, 0=down).",
	}, []string{"dependency"})

	dlqPending = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "nats_dlq_pending_messages",
		Help: "Messages sitting in the JOBS_DLQ dead-letter stream (dead-lettered jobs).",
	})
)

func boolGauge(up bool) float64 {
	if up {
		return 1
	}
	return 0
}

func setDependency(name string, up bool) { dependencyUp.WithLabelValues(name).Set(boolGauge(up)) }
func setDLQ(n float64)                   { dlqPending.Set(n) }

// Start launches the poller: it samples once immediately, then every
// OPS_METRICS_INTERVAL (default 20s) until ctx is cancelled.
func Start(ctx context.Context, db *gorm.DB) {
	interval := config.GetEnvDuration("OPS_METRICS_INTERVAL", 20*time.Second)
	if interval <= 0 {
		interval = 20 * time.Second
	}
	go func() {
		// Long-lived loop: recover so a panic doesn't silently kill ops metrics.
		defer func() {
			if r := recover(); r != nil {
				slog.Error("ops metrics poller panic", "error", fmt.Sprintf("panic: %v", r), "op", "opsmetrics.panic")
			}
		}()
		slog.Info("ops metrics poller started", "interval", interval.String())
		sample(ctx, db)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sample(ctx, db)
			}
		}
	}()
}

// sample probes each dependency and updates the gauges.
func sample(ctx context.Context, db *gorm.DB) {
	pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	// Redis.
	if redisstore.Client != nil {
		setDependency("redis", redisstore.Client.Ping(pctx).Err() == nil)
	} else {
		setDependency("redis", false)
	}

	// Postgres.
	if db != nil {
		setDependency("postgres", db.WithContext(pctx).Exec("SELECT 1").Error == nil)
	} else {
		setDependency("postgres", false)
	}

	// NATS liveness + DLQ depth in one path: a successful JOBS_DLQ Info proves
	// JetStream is reachable and yields the backlog count.
	natsUp := natsconn.Conn != nil && natsconn.Conn.IsConnected()
	if natsconn.JS != nil {
		if stream, err := natsconn.JS.Stream(pctx, dlqStreamName); err == nil {
			if info, ierr := stream.Info(pctx); ierr == nil {
				natsUp = true
				setDLQ(float64(info.State.Msgs))
			} else {
				slog.Warn("ops metrics: DLQ stream info failed", "error", ierr)
			}
		} else {
			slog.Warn("ops metrics: DLQ stream lookup failed", "error", err)
			natsUp = false
		}
	}
	setDependency("nats", natsUp)
}

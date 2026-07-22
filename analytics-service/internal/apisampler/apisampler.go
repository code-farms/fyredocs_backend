// Package apisampler periodically scrapes the API gateway's Prometheus metrics
// and persists per-interval samples of request volume, error classes, and
// latency percentiles into analytics_events' sibling table api_metric_samples.
// The /admin/metrics/api-trends handler reads these to render a time series —
// gateway Prometheus counters are cumulative-since-start with no history, so
// this sampler is what turns them into a trend.
package apisampler

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"

	"fyredocs/shared/config"

	"analytics-service/internal/models"
	"analytics-service/internal/promscrape"
)

// gatewayMetricsURL returns the gateway /metrics URL (shared with APIPerformance).
func gatewayMetricsURL() string {
	if u := strings.TrimSpace(os.Getenv("API_GATEWAY_METRICS_URL")); u != "" {
		return u
	}
	return "http://api-gateway:8080/metrics"
}

// computeSampleDeltas turns two consecutive cumulative scrapes into a per-interval
// sample. On a counter reset (current < previous, e.g. gateway restart) the
// current value is used as the delta. Latency columns are the current snapshot.
func computeSampleDeltas(prev, cur promscrape.HTTPAggregate) models.APIMetricSample {
	delta := func(p, c int64) int64 {
		if c < p {
			return c
		}
		return c - p
	}
	return models.APIMetricSample{
		Requests:     delta(prev.Requests, cur.Requests),
		ClientErrors: delta(prev.ClientErrors, cur.ClientErrors),
		ServerErrors: delta(prev.ServerErrors, cur.ServerErrors),
		Timeouts:     delta(prev.Timeouts, cur.Timeouts),
		AvgMs:        cur.AvgMs,
		P50Ms:        cur.P50Ms,
		P95Ms:        cur.P95Ms,
		P99Ms:        cur.P99Ms,
	}
}

// Start launches the background sampler. It scrapes once to establish a baseline,
// then writes a delta sample every API_SAMPLE_INTERVAL (default 60s) and prunes
// rows older than API_METRIC_RETENTION_DAYS (default 30). Stops when ctx is done.
func Start(ctx context.Context, db *gorm.DB) {
	if db == nil {
		slog.Warn("api metrics sampler disabled (no DB)")
		return
	}
	interval := config.GetEnvDuration("API_SAMPLE_INTERVAL", time.Minute)
	if interval <= 0 {
		interval = time.Minute
	}
	retentionDays := config.GetEnvInt("API_METRIC_RETENTION_DAYS", 30)
	if retentionDays <= 0 {
		retentionDays = 30
	}
	url := gatewayMetricsURL()

	scrape := func() (promscrape.HTTPAggregate, bool) {
		families, err := promscrape.MetricFamilies(ctx, url)
		if err != nil {
			slog.Warn("api metrics scrape failed", "url", url, "error", err)
			return promscrape.HTTPAggregate{}, false
		}
		return promscrape.AggregateHTTP(families), true
	}

	go func() {
		// Long-lived background loop: a panic here would silently kill the sampler
		// for the whole process lifetime. Recover and log so it's visible.
		defer func() {
			if r := recover(); r != nil {
				slog.Error("api metrics sampler panic", "error", fmt.Sprintf("panic: %v", r), "op", "apisampler.panic")
			}
		}()
		slog.Info("api metrics sampler started", "interval", interval.String(), "url", url)
		var last *promscrape.HTTPAggregate
		if cur, ok := scrape(); ok {
			last = &cur // baseline; no row emitted yet
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cur, ok := scrape()
				if !ok {
					continue
				}
				if last != nil {
					sample := computeSampleDeltas(*last, cur)
					sample.SampledAt = time.Now().UTC()
					if err := db.Create(&sample).Error; err != nil {
						slog.Warn("failed to persist api metric sample", "error", err)
					}
				}
				last = &cur
				cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)
				if err := db.Where("sampled_at < ?", cutoff).Delete(&models.APIMetricSample{}).Error; err != nil {
					slog.Warn("failed to prune api metric samples", "error", err)
				}
			}
		}
	}()
}

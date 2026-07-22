package handlers

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"analytics-service/internal/models"
	"fyredocs/shared/response"
)

// APITrends returns a time series of gateway HTTP metrics (request volume, error
// classes, latency percentiles) built from api_metric_samples written by the API
// metrics sampler. GET /admin/metrics/api-trends?days=N.
func APITrends(c *gin.Context) {
	days := queryInt(c, "days", 7)
	if days <= 0 {
		days = 7
	}
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -days)

	// Short ranges chart per-hour; longer ranges per-day.
	resolution := "day"
	if days <= 2 {
		resolution = "hour"
	}

	series := []gin.H{}
	errorClasses := []gin.H{}
	var totalReq, totalErr int64
	var avgAcc float64
	var sampledSince *string

	if rdb(c) != nil {
		type bucketRow struct {
			Time         time.Time
			Requests     int64
			ClientErrors int64
			ServerErrors int64
			Timeouts     int64
			AvgMs        float64
			P50Ms        float64
			P95Ms        float64
			P99Ms        float64
		}
		var rows []bucketRow
		// resolution is whitelisted ('hour'|'day'), never user input, so it is safe
		// to inline. It must NOT be a bound param: a parameterized DATE_TRUNC in both
		// SELECT and GROUP BY becomes two distinct params ($1/$4), which Postgres
		// rejects with "must appear in the GROUP BY clause". GROUP BY 1 (ordinal)
		// references the SELECT expression exactly.
		bucketQuery := fmt.Sprintf(`
			SELECT DATE_TRUNC('%s', sampled_at) as time,
				COALESCE(SUM(requests), 0) as requests,
				COALESCE(SUM(client_errors), 0) as client_errors,
				COALESCE(SUM(server_errors), 0) as server_errors,
				COALESCE(SUM(timeouts), 0) as timeouts,
				COALESCE(AVG(avg_ms), 0) as avg_ms,
				COALESCE(AVG(p50_ms), 0) as p50_ms,
				COALESCE(AVG(p95_ms), 0) as p95_ms,
				COALESCE(AVG(p99_ms), 0) as p99_ms
			FROM api_metric_samples
			WHERE sampled_at >= ? AND sampled_at < ?
			GROUP BY 1
			ORDER BY 1 ASC
		`, resolution)
		if err := rdb(c).Raw(bucketQuery, since, now).Scan(&rows).Error; err != nil {
			slog.Warn("api-trends bucket query failed", "error", err)
		}

		for _, r := range rows {
			errs := r.ClientErrors + r.ServerErrors + r.Timeouts
			rate := 0.0
			if r.Requests > 0 {
				rate = float64(errs) / float64(r.Requests)
			}
			t := r.Time.UTC().Format(time.RFC3339)
			series = append(series, gin.H{
				"time":      t,
				"requests":  r.Requests,
				"errors":    errs,
				"errorRate": rate,
				"avgMs":     r.AvgMs,
				"p50Ms":     r.P50Ms,
				"p95Ms":     r.P95Ms,
				"p99Ms":     r.P99Ms,
			})
			errorClasses = append(errorClasses, gin.H{
				"time":         t,
				"clientErrors": r.ClientErrors,
				"serverErrors": r.ServerErrors,
				"timeouts":     r.Timeouts,
			})
			totalReq += r.Requests
			totalErr += errs
			avgAcc += r.AvgMs
		}

		// Earliest sample overall drives the UI's "collecting since" hint.
		var minAt *time.Time
		rdb(c).Model(&models.APIMetricSample{}).Select("MIN(sampled_at)").Scan(&minAt)
		if minAt != nil && !minAt.IsZero() {
			s := minAt.UTC().Format(time.RFC3339)
			sampledSince = &s
		}
	}

	totalsRate := 0.0
	if totalReq > 0 {
		totalsRate = float64(totalErr) / float64(totalReq)
	}
	avgMs := 0.0
	if len(series) > 0 {
		avgMs = avgAcc / float64(len(series))
	}

	// Previous equal-length window for delta comparison.
	prev := gin.H{"requests": int64(0), "errors": int64(0), "errorRate": 0.0, "avgMs": 0.0}
	if rdb(c) != nil {
		var pv struct {
			Requests     int64
			ClientErrors int64
			ServerErrors int64
			Timeouts     int64
			AvgMs        float64
		}
		prevSince := since.AddDate(0, 0, -days)
		rdb(c).Raw(`
			SELECT COALESCE(SUM(requests), 0) as requests,
				COALESCE(SUM(client_errors), 0) as client_errors,
				COALESCE(SUM(server_errors), 0) as server_errors,
				COALESCE(SUM(timeouts), 0) as timeouts,
				COALESCE(AVG(avg_ms), 0) as avg_ms
			FROM api_metric_samples
			WHERE sampled_at >= ? AND sampled_at < ?
		`, prevSince, since).Scan(&pv)
		prevErr := pv.ClientErrors + pv.ServerErrors + pv.Timeouts
		prevRate := 0.0
		if pv.Requests > 0 {
			prevRate = float64(prevErr) / float64(pv.Requests)
		}
		prev = gin.H{"requests": pv.Requests, "errors": prevErr, "errorRate": prevRate, "avgMs": pv.AvgMs}
	}

	response.OK(c, "API trends retrieved", gin.H{
		"period":       gin.H{"from": since.Format(time.RFC3339), "to": now.Format(time.RFC3339), "days": days},
		"resolution":   resolution,
		"series":       series,
		"totals":       gin.H{"requests": totalReq, "errors": totalErr, "errorRate": totalsRate, "avgMs": avgMs},
		"previous":     prev,
		"errorClasses": errorClasses,
		"sampledSince": sampledSince,
	})
}

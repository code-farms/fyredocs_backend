package handlers

import (
	"time"

	"github.com/gin-gonic/gin"

	"analytics-service/internal/models"
	"fyredocs/shared/response"
)

// ReliabilityMetrics returns reliability metrics: job success/failure rates,
// error trends, processing time, tool-specific errors, and plan limit hits.
func ReliabilityMetrics(c *gin.Context) {
	days := queryInt(c, "days", 30)
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -days)

	// Job success/failure rate
	type jobRateResult struct {
		Completed int64 `json:"completed"`
		Failed    int64 `json:"failed"`
	}
	var jobRate jobRateResult
	models.DB.Raw(`
		SELECT
			COALESCE(SUM(CASE WHEN event_type = 'job.completed' THEN 1 ELSE 0 END), 0) as completed,
			COALESCE(SUM(CASE WHEN event_type = 'job.failed' THEN 1 ELSE 0 END), 0) as failed
		FROM analytics_events
		WHERE event_type IN ('job.completed', 'job.failed')
			AND created_at >= ? AND created_at < ?
	`, since, now).Scan(&jobRate)

	totalJobs := jobRate.Completed + jobRate.Failed
	var successRate float64
	if totalJobs > 0 {
		successRate = float64(jobRate.Completed) / float64(totalJobs)
	}

	// Error rate over time (daily)
	type errorRateRow struct {
		Date     string `json:"date"`
		Failures int64  `json:"failures"`
		Total    int64  `json:"total"`
	}
	var errorTrend []errorRateRow
	models.DB.Raw(`
		SELECT DATE(created_at) as date,
			SUM(CASE WHEN event_type = 'job.failed' THEN 1 ELSE 0 END) as failures,
			COUNT(*) as total
		FROM analytics_events
		WHERE event_type IN ('job.completed', 'job.failed')
			AND created_at >= ? AND created_at < ?
		GROUP BY DATE(created_at)
		ORDER BY date ASC
	`, since, now).Scan(&errorTrend)

	// Average processing time (p50, p95)
	type processingTimeResult struct {
		AvgSeconds float64 `json:"avgSeconds"`
		P50Seconds float64 `json:"p50Seconds"`
		P95Seconds float64 `json:"p95Seconds"`
	}
	var processingTime processingTimeResult
	models.DB.Raw(`
		SELECT
			COALESCE(AVG(EXTRACT(EPOCH FROM (completed.created_at - created.created_at))), 0) as avg_seconds,
			COALESCE(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (completed.created_at - created.created_at))), 0) as p50_seconds,
			COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (completed.created_at - created.created_at))), 0) as p95_seconds
		FROM analytics_events created
		JOIN analytics_events completed
			ON completed.job_id = created.job_id
		WHERE created.event_type = 'job.created'
			AND completed.event_type = 'job.completed'
			AND created.job_id IS NOT NULL
			AND completed.job_id IS NOT NULL
			AND created.created_at >= ? AND created.created_at < ?
	`, since, now).Scan(&processingTime)

	// Tool-specific error rates
	type toolErrorRow struct {
		ToolType  string `json:"toolType"`
		Completed int64  `json:"completed"`
		Failed    int64  `json:"failed"`
	}
	var toolErrors []toolErrorRow
	models.DB.Raw(`
		SELECT tool_type,
			SUM(CASE WHEN event_type = 'job.completed' THEN 1 ELSE 0 END) as completed,
			SUM(CASE WHEN event_type = 'job.failed' THEN 1 ELSE 0 END) as failed
		FROM analytics_events
		WHERE event_type IN ('job.completed', 'job.failed') AND tool_type != ''
			AND created_at >= ? AND created_at < ?
		GROUP BY tool_type
		ORDER BY failed DESC
	`, since, now).Scan(&toolErrors)

	// Daily processing-time percentiles (p50/p95/p99) for the latency trend chart.
	type latencyTrendRow struct {
		Date string  `json:"date"`
		P50  float64 `json:"p50"`
		P95  float64 `json:"p95"`
		P99  float64 `json:"p99"`
	}
	var latencyTrend []latencyTrendRow
	models.DB.Raw(`
		SELECT DATE(created.created_at) as date,
			COALESCE(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (completed.created_at - created.created_at))), 0) as p50,
			COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (completed.created_at - created.created_at))), 0) as p95,
			COALESCE(PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (completed.created_at - created.created_at))), 0) as p99
		FROM analytics_events created
		JOIN analytics_events completed
			ON completed.job_id = created.job_id
			AND completed.event_type = 'job.completed'
		WHERE created.event_type = 'job.created'
			AND created.job_id IS NOT NULL
			AND created.created_at >= ? AND created.created_at < ?
		GROUP BY DATE(created.created_at)
		ORDER BY date ASC
	`, since, now).Scan(&latencyTrend)

	// Daily failure counts bucketed by root-cause category, parsed from the
	// "[ERROR_CODE] message" prefix stored in metadata.failureReason (camelCase).
	type failureCategoryRow struct {
		Date     string `json:"date"`
		Category string `json:"category"`
		Count    int64  `json:"count"`
	}
	var failureCategories []failureCategoryRow
	models.DB.Raw(`
		SELECT DATE(created_at) as date,
			CASE substring(metadata->>'failureReason' FROM '^\[([A-Z_]+)\]')
				WHEN 'TIMEOUT'           THEN 'timeout'
				WHEN 'INVALID_PAYLOAD'   THEN 'validation'
				WHEN 'UNSUPPORTED_TOOL'  THEN 'validation'
				WHEN 'CONVERSION_FAILED' THEN 'processing'
				WHEN 'OUTPUT_FAILED'     THEN 'infrastructure'
				ELSE 'other'
			END as category,
			COUNT(*) as count
		FROM analytics_events
		WHERE event_type = 'job.failed'
			AND created_at >= ? AND created_at < ?
		GROUP BY DATE(created_at), category
		ORDER BY date ASC
	`, since, now).Scan(&failureCategories)

	// Plan limit hit frequency
	type limitHitRow struct {
		Date     string `json:"date"`
		PlanName string `json:"planName"`
		Hits     int64  `json:"hits"`
	}
	var limitHits []limitHitRow
	models.DB.Model(&models.AnalyticsEvent{}).
		Select("DATE(created_at) as date, plan_name, COUNT(*) as hits").
		Where("event_type = ? AND created_at >= ? AND created_at < ?", "plan.limit_hit", since, now).
		Group("DATE(created_at), plan_name").
		Order("date ASC").
		Scan(&limitHits)

	response.OK(c, "Reliability metrics retrieved", gin.H{
		"period": gin.H{
			"from": since.Format("2006-01-02"),
			"to":   now.Format("2006-01-02"),
			"days": days,
		},
		"jobRate": gin.H{
			"completed":   jobRate.Completed,
			"failed":      jobRate.Failed,
			"total":       totalJobs,
			"successRate": successRate,
		},
		"errorTrend": errorTrend,
		"processingTime": gin.H{
			"avgSeconds": processingTime.AvgSeconds,
			"p50Seconds": processingTime.P50Seconds,
			"p95Seconds": processingTime.P95Seconds,
		},
		"toolErrors":          toolErrors,
		"planLimitHits":       limitHits,
		"processingTimeTrend": latencyTrend,
		"failureCategories":   failureCategories,
	})
}

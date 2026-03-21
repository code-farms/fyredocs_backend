package handlers

import (
	"time"

	"github.com/gin-gonic/gin"

	"analytics-service/internal/models"
	"esydocs/shared/response"
)

// EngagementMetrics returns engagement metrics: tool adoption trends, jobs per user,
// file size distribution, guest vs registered usage, and power users.
func EngagementMetrics(c *gin.Context) {
	days := queryInt(c, "days", 30)
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -days)

	// Tool adoption with daily trends
	type toolTrendRow struct {
		Date     string `json:"date"`
		ToolType string `json:"toolType"`
		Count    int64  `json:"count"`
	}
	var toolTrends []toolTrendRow
	models.DB.Model(&models.AnalyticsEvent{}).
		Select("DATE(created_at) as date, tool_type, COUNT(*) as count").
		Where("tool_type != '' AND created_at >= ? AND created_at < ?", since, now).
		Group("DATE(created_at), tool_type").
		Order("date ASC, count DESC").
		Scan(&toolTrends)

	// Jobs per user (avg, median)
	type jobsPerUserResult struct {
		Average float64 `json:"average"`
		Median  float64 `json:"median"`
	}
	var jobsPerUser jobsPerUserResult
	models.DB.Raw(`
		SELECT
			COALESCE(AVG(job_count), 0) as average,
			COALESCE(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY job_count), 0) as median
		FROM (
			SELECT user_id, COUNT(*) as job_count
			FROM analytics_events
			WHERE event_type = 'job.created' AND user_id IS NOT NULL
				AND created_at >= ? AND created_at < ?
			GROUP BY user_id
		) sub
	`, since, now).Scan(&jobsPerUser)

	// File size distribution (histogram buckets)
	type fileSizeBucket struct {
		Bucket string `json:"bucket"`
		Count  int64  `json:"count"`
	}
	var fileSizeDist []fileSizeBucket
	models.DB.Raw(`
		SELECT
			CASE
				WHEN file_size < 1048576 THEN 'under_1mb'
				WHEN file_size < 5242880 THEN '1mb_5mb'
				WHEN file_size < 10485760 THEN '5mb_10mb'
				WHEN file_size < 52428800 THEN '10mb_50mb'
				ELSE 'over_50mb'
			END as bucket,
			COUNT(*) as count
		FROM analytics_events
		WHERE file_size > 0 AND created_at >= ? AND created_at < ?
		GROUP BY bucket
		ORDER BY MIN(file_size) ASC
	`, since, now).Scan(&fileSizeDist)

	// Guest vs registered usage
	type usageBreakdown struct {
		GuestEvents      int64 `json:"guestEvents"`
		RegisteredEvents int64 `json:"registeredEvents"`
		UniqueRegistered int64 `json:"uniqueRegistered"`
	}
	var usage usageBreakdown
	models.DB.Raw(`
		SELECT
			COALESCE(SUM(CASE WHEN is_guest = true THEN 1 ELSE 0 END), 0) as guest_events,
			COALESCE(SUM(CASE WHEN is_guest = false AND user_id IS NOT NULL THEN 1 ELSE 0 END), 0) as registered_events,
			COUNT(DISTINCT CASE WHEN is_guest = false AND user_id IS NOT NULL THEN user_id END) as unique_registered
		FROM analytics_events
		WHERE created_at >= ? AND created_at < ?
	`, since, now).Scan(&usage)

	var guestRatio float64
	totalEvents := usage.GuestEvents + usage.RegisteredEvents
	if totalEvents > 0 {
		guestRatio = float64(usage.GuestEvents) / float64(totalEvents)
	}

	// Power users (top 20 by job count)
	type powerUser struct {
		UserID   string `json:"userId"`
		JobCount int64  `json:"jobCount"`
	}
	var powerUsers []powerUser
	models.DB.Model(&models.AnalyticsEvent{}).
		Select("user_id, COUNT(*) as job_count").
		Where("event_type IN ('job.created','job.completed') AND user_id IS NOT NULL AND created_at >= ? AND created_at < ?", since, now).
		Group("user_id").
		Order("job_count DESC").
		Limit(20).
		Scan(&powerUsers)

	response.OK(c, "Engagement metrics retrieved", gin.H{
		"period": gin.H{
			"from": since.Format("2006-01-02"),
			"to":   now.Format("2006-01-02"),
			"days": days,
		},
		"toolTrends": toolTrends,
		"jobsPerUser": gin.H{
			"average": jobsPerUser.Average,
			"median":  jobsPerUser.Median,
		},
		"fileSizeDistribution": fileSizeDist,
		"guestVsRegistered": gin.H{
			"guestEvents":      usage.GuestEvents,
			"registeredEvents": usage.RegisteredEvents,
			"uniqueRegistered": usage.UniqueRegistered,
			"guestRatio":       guestRatio,
		},
		"powerUsers": powerUsers,
	})
}

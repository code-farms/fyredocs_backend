package handlers

import (
	"time"

	"github.com/gin-gonic/gin"

	"analytics-service/internal/models"
	"esydocs/shared/response"
)

// SystemHealth returns system health metrics: event ingestion rate,
// active users, processing lag, and event type breakdown.
func SystemHealth(c *gin.Context) {
	now := time.Now().UTC()

	// Event ingestion rate (hourly, last 24h)
	type ingestionRow struct {
		Hour  string `json:"hour"`
		Count int64  `json:"count"`
	}
	var ingestionRate []ingestionRow
	models.DB.Raw(`
		SELECT DATE_TRUNC('hour', created_at) as hour, COUNT(*) as count
		FROM analytics_events
		WHERE created_at >= ?
		GROUP BY DATE_TRUNC('hour', created_at)
		ORDER BY hour ASC
	`, now.Add(-24*time.Hour)).Scan(&ingestionRate)

	// Total events last 24h and last hour
	var eventsLast24h int64
	models.DB.Model(&models.AnalyticsEvent{}).
		Where("created_at >= ?", now.Add(-24*time.Hour)).
		Count(&eventsLast24h)

	var eventsLastHour int64
	models.DB.Model(&models.AnalyticsEvent{}).
		Where("created_at >= ?", now.Add(-1*time.Hour)).
		Count(&eventsLastHour)

	// Active users right now (last 5 minutes)
	var activeUsersNow int64
	models.DB.Model(&models.AnalyticsEvent{}).
		Where("user_id IS NOT NULL AND is_guest = false AND created_at >= ?", now.Add(-5*time.Minute)).
		Distinct("user_id").
		Count(&activeUsersNow)

	// Event processing lag (last hour)
	type lagResult struct {
		AvgLagSeconds float64 `json:"avgLagSeconds"`
		MaxLagSeconds float64 `json:"maxLagSeconds"`
	}
	var lag lagResult
	models.DB.Raw(`
		SELECT
			COALESCE(AVG(EXTRACT(EPOCH FROM (persisted_at - created_at))), 0) as avg_lag_seconds,
			COALESCE(MAX(EXTRACT(EPOCH FROM (persisted_at - created_at))), 0) as max_lag_seconds
		FROM analytics_events
		WHERE created_at >= ? AND persisted_at IS NOT NULL AND persisted_at > '0001-01-01'
	`, now.Add(-1*time.Hour)).Scan(&lag)

	// Events by type (last hour)
	type eventTypeRow struct {
		EventType string `json:"eventType"`
		Count     int64  `json:"count"`
	}
	var eventsByType []eventTypeRow
	models.DB.Model(&models.AnalyticsEvent{}).
		Select("event_type, COUNT(*) as count").
		Where("created_at >= ?", now.Add(-1*time.Hour)).
		Group("event_type").
		Order("count DESC").
		Scan(&eventsByType)

	// Total events in database
	var totalEvents int64
	models.DB.Model(&models.AnalyticsEvent{}).Count(&totalEvents)

	response.OK(c, "System health metrics retrieved", gin.H{
		"timestamp":      now.Format(time.RFC3339),
		"activeUsersNow": activeUsersNow,
		"eventsLastHour": eventsLastHour,
		"eventsLast24h":  eventsLast24h,
		"totalEvents":    totalEvents,
		"ingestionRate":  ingestionRate,
		"processingLag": gin.H{
			"avgSeconds": lag.AvgLagSeconds,
			"maxSeconds": lag.MaxLagSeconds,
		},
		"eventsByType": eventsByType,
	})
}

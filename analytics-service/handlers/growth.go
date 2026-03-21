package handlers

import (
	"time"

	"github.com/gin-gonic/gin"

	"analytics-service/internal/models"
	"esydocs/shared/response"
)

// GrowthMetrics returns product growth metrics: DAU/WAU/MAU, stickiness,
// activation rate, retention cohorts, and conversion funnel.
func GrowthMetrics(c *gin.Context) {
	days := queryInt(c, "days", 30)
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -days)

	// DAU trend
	type dauRow struct {
		Date string `json:"date"`
		DAU  int64  `json:"dau"`
	}
	var dauTrend []dauRow
	models.DB.Model(&models.AnalyticsEvent{}).
		Select("DATE(created_at) as date, COUNT(DISTINCT user_id) as dau").
		Where("user_id IS NOT NULL AND is_guest = false AND created_at >= ? AND created_at < ?", since, now).
		Group("DATE(created_at)").
		Order("date ASC").
		Scan(&dauTrend)

	// Current DAU (today)
	today := now.Truncate(24 * time.Hour)
	var dau int64
	models.DB.Model(&models.AnalyticsEvent{}).
		Where("user_id IS NOT NULL AND is_guest = false AND created_at >= ? AND created_at < ?", today, today.Add(24*time.Hour)).
		Distinct("user_id").
		Count(&dau)

	// WAU (last 7 days)
	var wau int64
	models.DB.Model(&models.AnalyticsEvent{}).
		Where("user_id IS NOT NULL AND is_guest = false AND created_at >= ?", now.AddDate(0, 0, -7)).
		Distinct("user_id").
		Count(&wau)

	// MAU (last 30 days)
	var mau int64
	models.DB.Model(&models.AnalyticsEvent{}).
		Where("user_id IS NOT NULL AND is_guest = false AND created_at >= ?", now.AddDate(0, 0, -30)).
		Distinct("user_id").
		Count(&mau)

	// Stickiness: avg DAU / MAU
	var stickiness float64
	if mau > 0 && len(dauTrend) > 0 {
		var totalDAU int64
		for _, d := range dauTrend {
			totalDAU += d.DAU
		}
		avgDAU := float64(totalDAU) / float64(len(dauTrend))
		stickiness = avgDAU / float64(mau)
	}

	// Activation rate: signups who created a job within 24h
	type activationResult struct {
		TotalSignups int64 `json:"totalSignups"`
		Activated    int64 `json:"activated"`
	}
	var activation activationResult
	models.DB.Raw(`
		SELECT
			COUNT(DISTINCT s.user_id) as total_signups,
			COUNT(DISTINCT j.user_id) as activated
		FROM analytics_events s
		LEFT JOIN analytics_events j ON j.user_id = s.user_id
			AND j.event_type = 'job.created'
			AND j.created_at >= s.created_at
			AND j.created_at <= s.created_at + INTERVAL '24 hours'
		WHERE s.event_type = 'user.signup'
			AND s.created_at >= ? AND s.created_at < ?
			AND s.user_id IS NOT NULL
	`, since, now).Scan(&activation)

	var activationRate float64
	if activation.TotalSignups > 0 {
		activationRate = float64(activation.Activated) / float64(activation.TotalSignups)
	}

	// Retention cohorts (D1, D7, D30)
	type cohortRow struct {
		CohortDate string `json:"cohortDate"`
		CohortSize int64  `json:"cohortSize"`
		D1         int64  `json:"d1"`
		D7         int64  `json:"d7"`
		D30        int64  `json:"d30"`
	}
	var cohorts []cohortRow
	models.DB.Raw(`
		WITH cohorts AS (
			SELECT user_id, DATE(MIN(created_at)) as signup_date
			FROM analytics_events
			WHERE event_type = 'user.signup' AND user_id IS NOT NULL
				AND created_at >= ? AND created_at < ?
			GROUP BY user_id
		),
		activity AS (
			SELECT DISTINCT user_id, DATE(created_at) as active_date
			FROM analytics_events
			WHERE user_id IS NOT NULL AND is_guest = false
				AND created_at >= ?
		)
		SELECT
			c.signup_date as cohort_date,
			COUNT(DISTINCT c.user_id) as cohort_size,
			COUNT(DISTINCT CASE WHEN a.active_date = c.signup_date + 1 THEN c.user_id END) as d1,
			COUNT(DISTINCT CASE WHEN a.active_date = c.signup_date + 7 THEN c.user_id END) as d7,
			COUNT(DISTINCT CASE WHEN a.active_date = c.signup_date + 30 THEN c.user_id END) as d30
		FROM cohorts c
		LEFT JOIN activity a ON a.user_id = c.user_id
		GROUP BY c.signup_date
		ORDER BY c.signup_date ASC
	`, since, now, since).Scan(&cohorts)

	// Conversion funnel: signup → job created → job completed → repeat user
	type funnelResult struct {
		SignedUp     int64 `json:"signedUp"`
		CreatedJob   int64 `json:"createdJob"`
		CompletedJob int64 `json:"completedJob"`
	}
	var funnel funnelResult
	models.DB.Raw(`
		SELECT
			COUNT(DISTINCT CASE WHEN event_type = 'user.signup' THEN user_id END) as signed_up,
			COUNT(DISTINCT CASE WHEN event_type = 'job.created' THEN user_id END) as created_job,
			COUNT(DISTINCT CASE WHEN event_type = 'job.completed' THEN user_id END) as completed_job
		FROM analytics_events
		WHERE user_id IS NOT NULL AND created_at >= ? AND created_at < ?
	`, since, now).Scan(&funnel)

	var repeatUsers int64
	models.DB.Raw(`
		SELECT COUNT(*) FROM (
			SELECT user_id FROM analytics_events
			WHERE event_type = 'job.completed' AND user_id IS NOT NULL
				AND created_at >= ? AND created_at < ?
			GROUP BY user_id HAVING COUNT(*) >= 2
		) sub
	`, since, now).Scan(&repeatUsers)

	response.OK(c, "Growth metrics retrieved", gin.H{
		"period": gin.H{
			"from": since.Format("2006-01-02"),
			"to":   now.Format("2006-01-02"),
			"days": days,
		},
		"dau":        dau,
		"wau":        wau,
		"mau":        mau,
		"stickiness": stickiness,
		"dauTrend":   dauTrend,
		"activationRate": gin.H{
			"signups":   activation.TotalSignups,
			"activated": activation.Activated,
			"rate":      activationRate,
		},
		"retention": cohorts,
		"funnel": gin.H{
			"signedUp":     funnel.SignedUp,
			"createdJob":   funnel.CreatedJob,
			"completedJob": funnel.CompletedJob,
			"repeatUser":   repeatUsers,
		},
	})
}

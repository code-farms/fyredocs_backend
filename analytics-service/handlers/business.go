package handlers

import (
	"time"

	"github.com/gin-gonic/gin"

	"analytics-service/internal/models"
	"esydocs/shared/response"
)

// BusinessMetrics returns business-level metrics: signups, plan changes, churn, conversion.
func BusinessMetrics(c *gin.Context) {
	days := queryInt(c, "days", 30)
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -days)
	periodEnd := now

	// Signups over time
	type signupRow struct {
		Date    string `json:"date"`
		Signups int64  `json:"signups"`
	}
	var signups []signupRow
	models.DB.Model(&models.AnalyticsEvent{}).
		Select("DATE(created_at) as date, COUNT(*) as signups").
		Where("event_type = ? AND created_at >= ? AND created_at < ?", "user.signup", since, periodEnd).
		Group("DATE(created_at)").
		Order("date ASC").
		Scan(&signups)

	var totalSignups int64
	for _, s := range signups {
		totalSignups += s.Signups
	}

	// Plan distribution over time
	type planTimeRow struct {
		Date     string `json:"date"`
		PlanName string `json:"planName"`
		Users    int64  `json:"users"`
	}
	var planDist []planTimeRow
	models.DB.Model(&models.AnalyticsEvent{}).
		Select("DATE(created_at) as date, plan_name, COUNT(DISTINCT user_id) as users").
		Where("created_at >= ? AND created_at < ? AND plan_name != '' AND user_id IS NOT NULL", since, periodEnd).
		Group("DATE(created_at), plan_name").
		Order("date ASC").
		Scan(&planDist)

	// Plan changes (upgrades/downgrades)
	type planChangeRow struct {
		Date    string `json:"date"`
		OldPlan string `json:"oldPlan"`
		NewPlan string `json:"newPlan"`
		Count   int64  `json:"count"`
	}
	var planChanges []planChangeRow
	models.DB.Model(&models.AnalyticsEvent{}).
		Select("DATE(created_at) as date, metadata->>'oldPlan' as old_plan, metadata->>'newPlan' as new_plan, COUNT(*) as count").
		Where("event_type = ? AND created_at >= ? AND created_at < ?", "plan.changed", since, periodEnd).
		Group("DATE(created_at), metadata->>'oldPlan', metadata->>'newPlan'").
		Order("date ASC").
		Scan(&planChanges)

	// Conversion rate (free → paid)
	var totalChanges int64
	var freeUpgrades int64
	models.DB.Model(&models.AnalyticsEvent{}).
		Where("event_type = ? AND created_at >= ? AND created_at < ?", "plan.changed", since, periodEnd).
		Count(&totalChanges)
	models.DB.Model(&models.AnalyticsEvent{}).
		Where("event_type = ? AND created_at >= ? AND created_at < ? AND metadata->>'oldPlan' = ?", "plan.changed", since, periodEnd, "free").
		Count(&freeUpgrades)

	var conversionRate float64
	if totalSignups > 0 {
		conversionRate = float64(freeUpgrades) / float64(totalSignups)
	}

	// Churn indicators: users active in previous period but not in current period
	inactiveDays := queryInt(c, "inactiveDays", 30)
	previousStart := since.AddDate(0, 0, -inactiveDays)

	var churnedUsers int64
	models.DB.Raw(`
		SELECT COUNT(DISTINCT prev.user_id)
		FROM analytics_events prev
		WHERE prev.user_id IS NOT NULL AND prev.is_guest = false
			AND prev.created_at >= ? AND prev.created_at < ?
			AND prev.user_id NOT IN (
				SELECT DISTINCT curr.user_id FROM analytics_events curr
				WHERE curr.user_id IS NOT NULL AND curr.is_guest = false
					AND curr.created_at >= ? AND curr.created_at < ?
			)
	`, previousStart, since, since, periodEnd).Scan(&churnedUsers)

	var previousActiveUsers int64
	models.DB.Model(&models.AnalyticsEvent{}).
		Where("user_id IS NOT NULL AND is_guest = false AND created_at >= ? AND created_at < ?", previousStart, since).
		Distinct("user_id").
		Count(&previousActiveUsers)

	var churnRate float64
	if previousActiveUsers > 0 {
		churnRate = float64(churnedUsers) / float64(previousActiveUsers)
	}

	response.OK(c, "Business metrics retrieved", gin.H{
		"period": gin.H{
			"from": since.Format("2006-01-02"),
			"to":   periodEnd.Format("2006-01-02"),
			"days": days,
		},
		"signups": gin.H{
			"total": totalSignups,
			"daily": signups,
		},
		"planDistribution": planDist,
		"planChanges":      planChanges,
		"conversionRate": gin.H{
			"totalChanges": totalChanges,
			"freeUpgrades": freeUpgrades,
			"rate":         conversionRate,
		},
		"churn": gin.H{
			"inactiveDays":       inactiveDays,
			"churnedUsers":       churnedUsers,
			"previousActiveUsers": previousActiveUsers,
			"churnRate":          churnRate,
		},
		"revenue": gin.H{
			"mrr":  nil,
			"arr":  nil,
			"cac":  nil,
			"ltv":  nil,
			"note": "Requires payment integration",
		},
	})
}

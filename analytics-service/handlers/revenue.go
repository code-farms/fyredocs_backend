package handlers

import (
	"time"

	"github.com/gin-gonic/gin"

	"analytics-service/internal/models"
	"analytics-service/internal/pricing"
	"fyredocs/shared/response"
)

// planCountRow is the active user count for a single plan at a point in time.
type planCountRow struct {
	PlanName string `json:"planName"`
	Users    int64  `json:"users"`
}

// planCountsAt returns the active-user-per-plan distribution as of `cutoff`,
// taking each user's most recent known plan from the event stream.
func planCountsAt(cutoff time.Time) []planCountRow {
	var rows []planCountRow
	models.DB.Raw(`
		SELECT plan_name, COUNT(*) as users FROM (
			SELECT DISTINCT ON (user_id) user_id, plan_name
			FROM analytics_events
			WHERE user_id IS NOT NULL AND is_guest = false AND plan_name <> ''
				AND created_at < ?
			ORDER BY user_id, created_at DESC
		) latest
		GROUP BY plan_name
	`, cutoff).Scan(&rows)
	return rows
}

// estimateMRR sums users × configured plan price for a distribution.
func estimateMRR(counts []planCountRow) float64 {
	var mrr float64
	for _, r := range counts {
		mrr += float64(r.Users) * pricing.PriceOf(r.PlanName)
	}
	return mrr
}

// RevenueMetrics returns ESTIMATED revenue derived from the active plan
// distribution and the configured PLAN_PRICES map. No billing integration.
func RevenueMetrics(c *gin.Context) {
	days := queryInt(c, "days", 30)
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -days)
	currency := pricing.Currency()
	prices := pricing.Prices()

	// Current distribution + MRR.
	current := planCountsAt(now)
	mrr := estimateMRR(current)
	previousMrr := estimateMRR(planCountsAt(since))
	byPlan := make([]gin.H, 0, len(current))
	for _, r := range current {
		price := pricing.PriceOf(r.PlanName)
		byPlan = append(byPlan, gin.H{
			"plan":          r.PlanName,
			"users":         r.Users,
			"pricePerMonth": price,
			"mrr":           float64(r.Users) * price,
		})
	}

	// Daily MRR trend via point-in-time plan reconstruction.
	type trendRow struct {
		Date     string `json:"date"`
		PlanName string `json:"planName"`
		Users    int64  `json:"users"`
	}
	var trendRows []trendRow
	models.DB.Raw(`
		WITH days AS (
			SELECT generate_series(?::date, ?::date, interval '1 day')::date AS day
		)
		SELECT d.day::text as date, lp.plan_name, COUNT(*) as users
		FROM days d
		CROSS JOIN LATERAL (
			SELECT DISTINCT ON (user_id) user_id, plan_name
			FROM analytics_events
			WHERE user_id IS NOT NULL AND is_guest = false AND plan_name <> ''
				AND created_at < d.day + 1
			ORDER BY user_id, created_at DESC
		) lp
		GROUP BY d.day, lp.plan_name
		ORDER BY d.day
	`, since, now).Scan(&trendRows)

	trendByDate := map[string]float64{}
	order := []string{}
	for _, r := range trendRows {
		if _, seen := trendByDate[r.Date]; !seen {
			order = append(order, r.Date)
		}
		trendByDate[r.Date] += float64(r.Users) * pricing.PriceOf(r.PlanName)
	}
	trend := make([]gin.H, 0, len(order))
	for _, date := range order {
		trend = append(trend, gin.H{"date": date, "mrr": trendByDate[date]})
	}

	// Plan changes: upgrades vs downgrades per day (ranked by price).
	type changeRow struct {
		Date    string `json:"date"`
		OldPlan string `json:"oldPlan"`
		NewPlan string `json:"newPlan"`
		Count   int64  `json:"count"`
	}
	var changeRows []changeRow
	models.DB.Raw(`
		SELECT DATE(created_at) as date,
			metadata->>'oldPlan' as old_plan,
			metadata->>'newPlan' as new_plan,
			COUNT(*) as count
		FROM analytics_events
		WHERE event_type = 'plan.changed'
			AND created_at >= ? AND created_at < ?
		GROUP BY DATE(created_at), metadata->>'oldPlan', metadata->>'newPlan'
		ORDER BY date ASC
	`, since, now).Scan(&changeRows)

	type upDown struct {
		up   int64
		down int64
	}
	changesByDate := map[string]*upDown{}
	changeOrder := []string{}
	for _, r := range changeRows {
		ud, ok := changesByDate[r.Date]
		if !ok {
			ud = &upDown{}
			changesByDate[r.Date] = ud
			changeOrder = append(changeOrder, r.Date)
		}
		if pricing.PriceOf(r.NewPlan) >= pricing.PriceOf(r.OldPlan) {
			ud.up += r.Count
		} else {
			ud.down += r.Count
		}
	}
	planChanges := make([]gin.H, 0, len(changeOrder))
	for _, date := range changeOrder {
		ud := changesByDate[date]
		planChanges = append(planChanges, gin.H{"date": date, "upgrades": ud.up, "downgrades": ud.down})
	}

	response.OK(c, "Estimated revenue retrieved", gin.H{
		"period":      gin.H{"from": since.Format("2006-01-02"), "to": now.Format("2006-01-02"), "days": days},
		"estimated":   true,
		"note":        "Estimated from plan distribution × configured PLAN_PRICES. No billing integration.",
		"currency":    currency,
		"prices":      prices,
		"mrr":         mrr,
		"arr":         mrr * 12,
		"previousMrr": previousMrr,
		"byPlan":      byPlan,
		"trend":       trend,
		"planChanges": planChanges,
	})
}

package handlers

import (
	"time"

	"github.com/gin-gonic/gin"

	"analytics-service/internal/models"
	"analytics-service/internal/pricing"
	"fyredocs/shared/response"
)

// sparkPoint is one point in a KPI sparkline.
type sparkPoint struct {
	Date  string  `json:"date"`
	Value float64 `json:"value"`
}

// kpi builds the {current, previous, sparkline} shape consumed by KPI cards.
// Pass current/previous as nil to signal "not yet available".
func kpi(current, previous *float64, spark []sparkPoint) gin.H {
	return gin.H{"current": current, "previous": previous, "sparkline": spark}
}

func f64(v float64) *float64 { return &v }

// ExecutiveOverview powers the 8 KPI cards with current value, previous-period
// value, and a daily sparkline for each metric.
func ExecutiveOverview(c *gin.Context) {
	days := queryInt(c, "days", 30)
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -days)
	prevStart := now.AddDate(0, 0, -2*days)
	sinceStr := since.Format("2006-01-02")

	// Daily aggregation across the current + previous window.
	type dailyRow struct {
		Date          string `json:"date"`
		Signups       int64  `json:"signups"`
		DAU           int64  `json:"dau"`
		JobsCreated   int64  `json:"jobsCreated"`
		JobsCompleted int64  `json:"jobsCompleted"`
		JobsFailed    int64  `json:"jobsFailed"`
	}
	var rows []dailyRow
	models.DB.Raw(`
		SELECT DATE(created_at) as date,
			COUNT(*) FILTER (WHERE event_type = 'user.signup') as signups,
			COUNT(DISTINCT user_id) FILTER (WHERE user_id IS NOT NULL AND is_guest = false) as dau,
			COUNT(*) FILTER (WHERE event_type = 'job.created') as jobs_created,
			COUNT(*) FILTER (WHERE event_type = 'job.completed') as jobs_completed,
			COUNT(*) FILTER (WHERE event_type = 'job.failed') as jobs_failed
		FROM analytics_events
		WHERE created_at >= ? AND created_at < ?
		GROUP BY DATE(created_at)
		ORDER BY date ASC
	`, prevStart, now).Scan(&rows)

	var curSignups, prevSignups, curJobs, prevJobs float64
	var curCompleted, curFailed, prevCompleted, prevFailed float64
	signupSpark := []sparkPoint{}
	dauSpark := []sparkPoint{}
	jobsSpark := []sparkPoint{}
	successSpark := []sparkPoint{}

	for _, r := range rows {
		current := r.Date >= sinceStr
		if current {
			curSignups += float64(r.Signups)
			curJobs += float64(r.JobsCreated)
			curCompleted += float64(r.JobsCompleted)
			curFailed += float64(r.JobsFailed)
			signupSpark = append(signupSpark, sparkPoint{r.Date, float64(r.Signups)})
			dauSpark = append(dauSpark, sparkPoint{r.Date, float64(r.DAU)})
			jobsSpark = append(jobsSpark, sparkPoint{r.Date, float64(r.JobsCreated)})
			dayTotal := r.JobsCompleted + r.JobsFailed
			var rate float64
			if dayTotal > 0 {
				rate = float64(r.JobsCompleted) / float64(dayTotal)
			}
			successSpark = append(successSpark, sparkPoint{r.Date, rate})
		} else {
			prevSignups += float64(r.Signups)
			prevJobs += float64(r.JobsCreated)
			prevCompleted += float64(r.JobsCompleted)
			prevFailed += float64(r.JobsFailed)
		}
	}

	// Distinct active users per period (distinct counts can't be summed by day).
	activeCurrent := distinctActiveUsers(since, now)
	activePrevious := distinctActiveUsers(prevStart, since)

	// Cumulative total users (distinct signup users) at each boundary.
	totalCurrent := distinctSignupUsers(now)
	totalPrevious := distinctSignupUsers(since)
	totalSpark := make([]sparkPoint, 0, len(signupSpark))
	running := float64(totalPrevious)
	for _, p := range signupSpark {
		running += p.Value
		totalSpark = append(totalSpark, sparkPoint{p.Date, running})
	}

	successCurrent := ratio(curCompleted, curCompleted+curFailed)
	successPrevious := ratio(prevCompleted, prevCompleted+prevFailed)

	// Estimated revenue (MRR) now vs at the start of the period.
	mrrCurrent := estimateMRR(planCountsAt(now))
	mrrPrevious := estimateMRR(planCountsAt(since))

	// Active servers — live health scrape (current snapshot only).
	ctx := c.Request.Context()
	services := scrapeServicesParallel(ctx, parseServiceURLs())
	services["analytics-service"] = getSelfServiceInfo()
	serverList := make([]gin.H, 0, len(services))
	healthy := 0
	for name, info := range services {
		status := "unhealthy"
		if s, ok := info["status"].(string); ok {
			status = s
		}
		if status == "healthy" {
			healthy++
		}
		serverList = append(serverList, gin.H{"name": name, "status": status})
	}

	response.OK(c, "Executive overview retrieved", gin.H{
		"period":      gin.H{"from": sinceStr, "to": now.Format("2006-01-02"), "days": days},
		"totalUsers":  kpi(f64(float64(totalCurrent)), f64(float64(totalPrevious)), totalSpark),
		"activeUsers": kpi(f64(float64(activeCurrent)), f64(float64(activePrevious)), dauSpark),
		"revenue": gin.H{
			"current": mrrCurrent, "previous": mrrPrevious, "sparkline": []sparkPoint{},
			"estimated": true, "currency": pricing.Currency(),
		},
		"jobsCreated":  kpi(f64(curJobs), f64(prevJobs), jobsSpark),
		"successRate":  kpi(f64(successCurrent), f64(successPrevious), successSpark),
		"apiRequests":  kpi(nil, nil, []sparkPoint{}),
		"apiErrorRate": kpi(nil, nil, []sparkPoint{}),
		"activeServers": gin.H{
			"current":  healthy,
			"total":    len(services),
			"services": serverList,
		},
	})
}

func distinctActiveUsers(from, to time.Time) int64 {
	var count int64
	models.DB.Raw(`
		SELECT COUNT(DISTINCT user_id) FROM analytics_events
		WHERE user_id IS NOT NULL AND is_guest = false
			AND created_at >= ? AND created_at < ?
	`, from, to).Scan(&count)
	return count
}

func distinctSignupUsers(before time.Time) int64 {
	var count int64
	models.DB.Raw(`
		SELECT COUNT(DISTINCT user_id) FROM analytics_events
		WHERE event_type = 'user.signup' AND user_id IS NOT NULL AND created_at < ?
	`, before).Scan(&count)
	return count
}

func ratio(num, den float64) float64 {
	if den <= 0 {
		return 0
	}
	return num / den
}

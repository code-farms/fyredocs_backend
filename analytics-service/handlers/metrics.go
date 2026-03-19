package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"analytics-service/internal/models"
	"esydocs/shared/response"
)

// Overview returns today's summary metrics.
func Overview(c *gin.Context) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	tomorrow := today.Add(24 * time.Hour)

	type countResult struct {
		Count int64
	}

	var signups int64
	models.DB.Model(&models.AnalyticsEvent{}).
		Where("event_type = ? AND created_at >= ? AND created_at < ?", "user.signup", today, tomorrow).
		Count(&signups)

	var logins int64
	models.DB.Model(&models.AnalyticsEvent{}).
		Where("event_type = ? AND created_at >= ? AND created_at < ?", "user.login", today, tomorrow).
		Count(&logins)

	var jobsCreated int64
	models.DB.Model(&models.AnalyticsEvent{}).
		Where("event_type = ? AND created_at >= ? AND created_at < ?", "job.created", today, tomorrow).
		Count(&jobsCreated)

	var jobsCompleted int64
	models.DB.Model(&models.AnalyticsEvent{}).
		Where("event_type = ? AND created_at >= ? AND created_at < ?", "job.completed", today, tomorrow).
		Count(&jobsCompleted)

	var jobsFailed int64
	models.DB.Model(&models.AnalyticsEvent{}).
		Where("event_type = ? AND created_at >= ? AND created_at < ?", "job.failed", today, tomorrow).
		Count(&jobsFailed)

	var planLimitHits int64
	models.DB.Model(&models.AnalyticsEvent{}).
		Where("event_type = ? AND created_at >= ? AND created_at < ?", "plan.limit_hit", today, tomorrow).
		Count(&planLimitHits)

	// DAU: distinct non-guest user IDs across all events today
	var dau int64
	models.DB.Model(&models.AnalyticsEvent{}).
		Where("created_at >= ? AND created_at < ? AND user_id IS NOT NULL AND is_guest = false", today, tomorrow).
		Distinct("user_id").
		Count(&dau)

	// Guest sessions today
	var guestSessions int64
	models.DB.Model(&models.AnalyticsEvent{}).
		Where("created_at >= ? AND created_at < ? AND is_guest = true", today, tomorrow).
		Count(&guestSessions)

	response.OK(c, "Overview retrieved", gin.H{
		"date":          today.Format("2006-01-02"),
		"signups":       signups,
		"logins":        logins,
		"dau":           dau,
		"guestSessions": guestSessions,
		"jobsCreated":   jobsCreated,
		"jobsCompleted": jobsCompleted,
		"jobsFailed":    jobsFailed,
		"planLimitHits": planLimitHits,
	})
}

// Daily returns daily aggregated metrics for a date range.
func Daily(c *gin.Context) {
	from := c.DefaultQuery("from", time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02"))
	to := c.DefaultQuery("to", time.Now().UTC().Format("2006-01-02"))

	fromDate, err := time.Parse("2006-01-02", from)
	if err != nil {
		response.BadRequest(c, "INVALID_INPUT", "invalid 'from' date format, use YYYY-MM-DD")
		return
	}
	toDate, err := time.Parse("2006-01-02", to)
	if err != nil {
		response.BadRequest(c, "INVALID_INPUT", "invalid 'to' date format, use YYYY-MM-DD")
		return
	}
	toDate = toDate.Add(24 * time.Hour) // inclusive end

	type dailyRow struct {
		Date      string `json:"date"`
		EventType string `json:"eventType"`
		Count     int64  `json:"count"`
	}

	var rows []dailyRow
	models.DB.Model(&models.AnalyticsEvent{}).
		Select("DATE(created_at) as date, event_type, COUNT(*) as count").
		Where("created_at >= ? AND created_at < ?", fromDate, toDate).
		Group("DATE(created_at), event_type").
		Order("date ASC").
		Scan(&rows)

	response.OK(c, "Daily metrics retrieved", gin.H{
		"from": from,
		"to":   to,
		"rows": rows,
	})
}

// ToolUsage returns tool usage breakdown.
func ToolUsage(c *gin.Context) {
	days := queryInt(c, "days", 30)
	since := time.Now().UTC().AddDate(0, 0, -days)

	type toolRow struct {
		ToolType  string `json:"toolType"`
		Count     int64  `json:"count"`
		Completed int64  `json:"completed"`
		Failed    int64  `json:"failed"`
	}

	var rows []toolRow
	models.DB.Model(&models.AnalyticsEvent{}).
		Select(`tool_type,
			COUNT(*) as count,
			SUM(CASE WHEN event_type = 'job.completed' THEN 1 ELSE 0 END) as completed,
			SUM(CASE WHEN event_type = 'job.failed' THEN 1 ELSE 0 END) as failed`).
		Where("tool_type != '' AND created_at >= ?", since).
		Group("tool_type").
		Order("count DESC").
		Scan(&rows)

	response.OK(c, "Tool usage retrieved", gin.H{
		"days": days,
		"rows": rows,
	})
}

// UserGrowth returns user signup growth over time.
func UserGrowth(c *gin.Context) {
	days := queryInt(c, "days", 90)
	since := time.Now().UTC().AddDate(0, 0, -days)

	type growthRow struct {
		Date    string `json:"date"`
		Signups int64  `json:"signups"`
		DAU     int64  `json:"dau"`
	}

	var rows []growthRow
	models.DB.Model(&models.AnalyticsEvent{}).
		Select(`DATE(created_at) as date,
			SUM(CASE WHEN event_type = 'user.signup' THEN 1 ELSE 0 END) as signups,
			COUNT(DISTINCT CASE WHEN user_id IS NOT NULL AND is_guest = false THEN user_id END) as dau`).
		Where("created_at >= ?", since).
		Group("DATE(created_at)").
		Order("date ASC").
		Scan(&rows)

	response.OK(c, "User growth retrieved", gin.H{
		"days": days,
		"rows": rows,
	})
}

// PlanDistribution returns the breakdown of events by plan type.
func PlanDistribution(c *gin.Context) {
	days := queryInt(c, "days", 30)
	since := time.Now().UTC().AddDate(0, 0, -days)

	type planRow struct {
		PlanName   string `json:"planName"`
		Users      int64  `json:"users"`
		Jobs       int64  `json:"jobs"`
		LimitHits  int64  `json:"limitHits"`
	}

	var rows []planRow
	models.DB.Model(&models.AnalyticsEvent{}).
		Select(`plan_name,
			COUNT(DISTINCT CASE WHEN user_id IS NOT NULL THEN user_id END) as users,
			SUM(CASE WHEN event_type IN ('job.created','job.completed','job.failed') THEN 1 ELSE 0 END) as jobs,
			SUM(CASE WHEN event_type = 'plan.limit_hit' THEN 1 ELSE 0 END) as limit_hits`).
		Where("plan_name != '' AND created_at >= ?", since).
		Group("plan_name").
		Scan(&rows)

	response.OK(c, "Plan distribution retrieved", gin.H{
		"days": days,
		"rows": rows,
	})
}

// Realtime returns metrics from the last hour.
func Realtime(c *gin.Context) {
	oneHourAgo := time.Now().UTC().Add(-1 * time.Hour)

	type realtimeRow struct {
		EventType string `json:"eventType"`
		Count     int64  `json:"count"`
	}

	var rows []realtimeRow
	models.DB.Model(&models.AnalyticsEvent{}).
		Select("event_type, COUNT(*) as count").
		Where("created_at >= ?", oneHourAgo).
		Group("event_type").
		Scan(&rows)

	response.OK(c, "Realtime metrics retrieved", gin.H{
		"since": oneHourAgo.Format(time.RFC3339),
		"rows":  rows,
	})
}

func queryInt(c *gin.Context, key string, fallback int) int {
	val := c.Query(key)
	if val == "" {
		return fallback
	}
	n := 0
	for _, ch := range val {
		if ch < '0' || ch > '9' {
			return fallback
		}
		n = n*10 + int(ch-'0')
	}
	if n <= 0 {
		return fallback
	}
	return n
}

// GetEvents returns raw analytics events with pagination for debugging.
func GetEvents(c *gin.Context) {
	limit := queryInt(c, "limit", 50)
	if limit > 200 {
		limit = 200
	}
	page := queryInt(c, "page", 1)
	offset := (page - 1) * limit

	eventType := c.Query("eventType")

	query := models.DB.Model(&models.AnalyticsEvent{})
	if eventType != "" {
		query = query.Where("event_type = ?", eventType)
	}

	var total int64
	query.Count(&total)

	var events []models.AnalyticsEvent
	query.Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&events)

	response.OKWithMeta(c, "Events retrieved", events, &response.Meta{
		Page:  page,
		Limit: limit,
		Total: total,
	})
}

// HealthCheck reports service health.
func HealthCheck(c *gin.Context) {
	c.String(http.StatusOK, "ok")
}

// ReadyCheck reports service readiness including dependency checks.
func ReadyCheck(c *gin.Context) {
	checks := gin.H{}
	ready := true

	if err := models.DB.Exec("SELECT 1").Error; err != nil {
		checks["postgres"] = err.Error()
		ready = false
	} else {
		checks["postgres"] = "ok"
	}

	if !ready {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready", "checks": checks})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready", "checks": checks})
}

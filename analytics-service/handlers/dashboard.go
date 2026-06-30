package handlers

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"analytics-service/internal/models"
	"fyredocs/shared/response"
)

// Dashboard is the unified, role-aware landing endpoint. Every authenticated
// user hits the same path; the payload is filtered server-side by role:
//   - admin / super-admin  -> system-wide KPIs
//   - regular user         -> personal KPIs scoped to their own user_id
//
// It reads only analytics-service's own analytics_events table (no
// cross-service calls). Role/identity come from the gateway-set headers, the
// same way adminAuth() reads them.
//
// GET /api/dashboard
func Dashboard(c *gin.Context) {
	role := strings.TrimSpace(c.GetHeader("X-User-Role"))
	userID := strings.TrimSpace(c.GetHeader("X-User-ID"))

	if userID == "" {
		response.Err(c, http.StatusUnauthorized, "UNAUTHORIZED", "Please log in to view your dashboard.")
		return
	}
	if role == "guest" {
		response.Err(c, http.StatusForbidden, "FORBIDDEN", "Guests do not have a dashboard.")
		return
	}

	if role == "admin" || role == "super-admin" {
		adminDashboard(c)
		return
	}
	userDashboard(c, userID)
}

// adminDashboard returns a curated system-wide summary, reusing the query
// shapes from Overview/ToolUsage/PlanDistribution.
func adminDashboard(c *gin.Context) {
	days := queryInt(c, "days", 30)
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -days)
	today := now.Truncate(24 * time.Hour)
	tomorrow := today.Add(24 * time.Hour)

	countEvents := func(eventType string, from, to time.Time) int64 {
		var n int64
		models.DB.Model(&models.AnalyticsEvent{}).
			Where("event_type = ? AND created_at >= ? AND created_at < ?", eventType, from, to).
			Count(&n)
		return n
	}

	type toolRow struct {
		ToolType  string `json:"toolType"`
		Count     int64  `json:"count"`
		Completed int64  `json:"completed"`
		Failed    int64  `json:"failed"`
	}
	type planRow struct {
		PlanName string `json:"planName"`
		Users    int64  `json:"users"`
		Jobs     int64  `json:"jobs"`
	}

	var (
		signups, logins                        int64
		jobsCreated, jobsCompleted, jobsFailed int64
		dau, guestSessions, totalUsers         int64
		byTool                                 []toolRow
		byPlan                                 []planRow
	)

	// All independent reads of analytics_events; the DB is remote so run them
	// concurrently to collapse ~10 sequential round-trips into ~1. Each closure
	// writes only its own variable.
	parallelQueries(
		func() { signups = countEvents("user.signup", today, tomorrow) },
		func() { logins = countEvents("user.login", today, tomorrow) },
		func() { jobsCreated = countEvents("job.created", today, tomorrow) },
		func() { jobsCompleted = countEvents("job.completed", today, tomorrow) },
		func() { jobsFailed = countEvents("job.failed", today, tomorrow) },
		func() {
			models.DB.Model(&models.AnalyticsEvent{}).
				Where("created_at >= ? AND created_at < ? AND user_id IS NOT NULL AND is_guest = false", today, tomorrow).
				Distinct("user_id").Count(&dau)
		},
		func() {
			models.DB.Model(&models.AnalyticsEvent{}).
				Where("created_at >= ? AND created_at < ? AND is_guest = true", today, tomorrow).
				Count(&guestSessions)
		},
		func() {
			models.DB.Model(&models.AnalyticsEvent{}).
				Where("created_at >= ? AND user_id IS NOT NULL AND is_guest = false", since).
				Distinct("user_id").Count(&totalUsers)
		},
		func() {
			models.DB.Model(&models.AnalyticsEvent{}).
				Select(`tool_type,
				COUNT(*) as count,
				SUM(CASE WHEN event_type = 'job.completed' THEN 1 ELSE 0 END) as completed,
				SUM(CASE WHEN event_type = 'job.failed' THEN 1 ELSE 0 END) as failed`).
				Where("tool_type != '' AND created_at >= ?", since).
				Group("tool_type").Order("count DESC").Limit(10).Scan(&byTool)
		},
		func() {
			models.DB.Model(&models.AnalyticsEvent{}).
				Select(`plan_name,
				COUNT(DISTINCT CASE WHEN user_id IS NOT NULL THEN user_id END) as users,
				SUM(CASE WHEN event_type IN ('job.created','job.completed','job.failed') THEN 1 ELSE 0 END) as jobs`).
				Where("plan_name != '' AND created_at >= ?", since).
				Group("plan_name").Scan(&byPlan)
		},
	)

	response.OK(c, "Dashboard retrieved", gin.H{
		"role": "admin",
		"period": gin.H{
			"days": days,
			"from": since.Format("2006-01-02"),
			"to":   now.Format("2006-01-02"),
		},
		"today": gin.H{
			"date":          today.Format("2006-01-02"),
			"signups":       signups,
			"logins":        logins,
			"dau":           dau,
			"guestSessions": guestSessions,
			"jobsCreated":   jobsCreated,
			"jobsCompleted": jobsCompleted,
			"jobsFailed":    jobsFailed,
		},
		"totalUsers":       totalUsers,
		"toolUsage":        byTool,
		"planDistribution": byPlan,
	})
}

// parallelQueries runs independent, side-effect-free DB queries concurrently and
// blocks until all complete. The backing DB is remote, so a network round-trip
// dominates each query; running N of them concurrently collapses N sequential
// round-trips into roughly one. Callers must ensure each fn writes only its own
// distinct variables (no shared mutable state) — gorm's root *DB is safe for
// concurrent use, so each fn building a fresh statement off models.DB is fine.
func parallelQueries(fns ...func()) {
	var wg sync.WaitGroup
	wg.Add(len(fns))
	for _, fn := range fns {
		go func(f func()) {
			defer wg.Done()
			f()
		}(fn)
	}
	wg.Wait()
}

// userDashboard returns personal KPIs scoped to one user_id.
func userDashboard(c *gin.Context, userIDStr string) {
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		response.Err(c, http.StatusUnauthorized, "UNAUTHORIZED", "Please log in to view your dashboard.")
		return
	}

	days := queryInt(c, "days", 30)
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -days)

	countOwned := func(eventType string) int64 {
		var n int64
		models.DB.Model(&models.AnalyticsEvent{}).
			Where("user_id = ? AND event_type = ?", userID, eventType).
			Count(&n)
		return n
	}

	type toolRow struct {
		ToolType  string `json:"toolType"`
		Count     int64  `json:"count"`
		Completed int64  `json:"completed"`
		Failed    int64  `json:"failed"`
	}
	type activityRow struct {
		Date  string `json:"date"`
		Count int64  `json:"count"`
	}

	var (
		totalJobs, completed, failed int64
		bytesProcessed               int64
		byTool                       []toolRow
		recentActivity               []activityRow
		plan                         string
		memberSince                  *time.Time
	)

	// These queries are independent reads of the analytics_events table. The DB
	// is remote (a round-trip dominates each one), so run them concurrently and
	// collapse N sequential round-trips into ~1. Each goroutine writes its own
	// variable, so there is no shared mutable state to guard.
	parallelQueries(
		func() { totalJobs = countOwned("job.created") },
		func() { completed = countOwned("job.completed") },
		func() { failed = countOwned("job.failed") },
		func() {
			models.DB.Model(&models.AnalyticsEvent{}).
				Where("user_id = ?", userID).
				Select("COALESCE(SUM(file_size), 0)").Scan(&bytesProcessed)
		},
		func() {
			models.DB.Model(&models.AnalyticsEvent{}).
				Select(`tool_type,
				COUNT(*) as count,
				SUM(CASE WHEN event_type = 'job.completed' THEN 1 ELSE 0 END) as completed,
				SUM(CASE WHEN event_type = 'job.failed' THEN 1 ELSE 0 END) as failed`).
				Where("user_id = ? AND tool_type != '' AND created_at >= ?", userID, since).
				Group("tool_type").Order("count DESC").Scan(&byTool)
		},
		func() {
			models.DB.Model(&models.AnalyticsEvent{}).
				Select("DATE(created_at) as date, COUNT(*) as count").
				Where("user_id = ? AND created_at >= ?", userID, since).
				Group("DATE(created_at)").Order("date ASC").Scan(&recentActivity)
		},
		func() {
			// Current plan: prefer the gateway-supplied header, fall back to the
			// latest plan_name recorded in this user's events.
			plan = strings.TrimSpace(c.GetHeader("X-User-Plan"))
			if plan == "" {
				var latest models.AnalyticsEvent
				if err := models.DB.
					Where("user_id = ? AND plan_name != ''", userID).
					Order("created_at DESC").First(&latest).Error; err == nil {
					plan = latest.PlanName
				}
			}
		},
		func() {
			var earliest models.AnalyticsEvent
			if err := models.DB.
				Where("user_id = ?", userID).
				Order("created_at ASC").First(&earliest).Error; err == nil {
				memberSince = &earliest.CreatedAt
			}
		},
	)

	response.OK(c, "Dashboard retrieved", gin.H{
		"role": "user",
		"period": gin.H{
			"days": days,
			"from": since.Format("2006-01-02"),
			"to":   now.Format("2006-01-02"),
		},
		"jobs": gin.H{
			"total":     totalJobs,
			"completed": completed,
			"failed":    failed,
		},
		"bytesProcessed": bytesProcessed,
		"toolUsage":      byTool,
		"recentActivity": recentActivity,
		"plan":           plan,
		"memberSince":    memberSince,
	})
}

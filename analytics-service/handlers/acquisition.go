package handlers

import (
	"time"

	"github.com/gin-gonic/gin"

	"analytics-service/internal/models"
	"fyredocs/shared/response"
)

// channelCaseSQL classifies a signup into an acquisition channel from the
// referrer/UTM metadata captured at signup. Rows with no such metadata (legacy
// signups, or before capture is deployed) fall into "unknown".
const channelCaseSQL = `
	CASE
		WHEN metadata IS NULL OR NOT (jsonb_exists(metadata, 'referrer') OR jsonb_exists(metadata, 'utmSource')) THEN 'unknown'
		WHEN COALESCE(metadata->>'utmMedium', '') IN ('cpc', 'ppc', 'paid', 'display', 'paid_social') THEN 'paid'
		WHEN COALESCE(metadata->>'utmSource', '') <> '' THEN 'campaign'
		WHEN COALESCE(metadata->>'referrer', '') = '' THEN 'direct'
		WHEN metadata->>'referrer' ~* '(google|bing|duckduckgo|yahoo)\.' THEN 'organic'
		ELSE 'referral'
	END`

// AcquisitionMetrics returns signup counts grouped by acquisition channel,
// daily and in aggregate, with a previous-period comparison.
func AcquisitionMetrics(c *gin.Context) {
	days := queryInt(c, "days", 30)
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -days)
	prevStart := now.AddDate(0, 0, -2*days)

	type dailyRow struct {
		Date    string `json:"date"`
		Channel string `json:"channel"`
		Signups int64  `json:"signups"`
	}
	var daily []dailyRow
	models.DB.Raw(`
		SELECT DATE(created_at) as date, `+channelCaseSQL+` as channel, COUNT(*) as signups
		FROM analytics_events
		WHERE event_type = 'user.signup' AND created_at >= ? AND created_at < ?
		GROUP BY DATE(created_at), channel
		ORDER BY date ASC
	`, since, now).Scan(&daily)

	// Aggregate current totals per channel from the daily rows.
	type channelRow struct {
		Channel string  `json:"channel"`
		Signups int64   `json:"signups"`
		Percent float64 `json:"percent"`
	}
	totals := map[string]int64{}
	order := []string{}
	var grandTotal int64
	for _, r := range daily {
		if _, seen := totals[r.Channel]; !seen {
			order = append(order, r.Channel)
		}
		totals[r.Channel] += r.Signups
		grandTotal += r.Signups
	}
	channels := make([]channelRow, 0, len(order))
	for _, ch := range order {
		pct := 0.0
		if grandTotal > 0 {
			pct = float64(totals[ch]) / float64(grandTotal) * 100
		}
		channels = append(channels, channelRow{Channel: ch, Signups: totals[ch], Percent: pct})
	}

	// Previous-period channel totals for comparison.
	type prevRow struct {
		Channel string `json:"channel"`
		Signups int64  `json:"signups"`
	}
	var prevChannels []prevRow
	models.DB.Raw(`
		SELECT `+channelCaseSQL+` as channel, COUNT(*) as signups
		FROM analytics_events
		WHERE event_type = 'user.signup' AND created_at >= ? AND created_at < ?
		GROUP BY channel
		ORDER BY signups DESC
	`, prevStart, since).Scan(&prevChannels)

	// Top referrers (non-empty referrer host).
	type referrerRow struct {
		Referrer string `json:"referrer"`
		Signups  int64  `json:"signups"`
	}
	var topReferrers []referrerRow
	models.DB.Raw(`
		SELECT metadata->>'referrer' as referrer, COUNT(*) as signups
		FROM analytics_events
		WHERE event_type = 'user.signup' AND created_at >= ? AND created_at < ?
			AND COALESCE(metadata->>'referrer', '') <> ''
		GROUP BY metadata->>'referrer'
		ORDER BY signups DESC
		LIMIT 10
	`, since, now).Scan(&topReferrers)

	response.OK(c, "Acquisition metrics retrieved", gin.H{
		"period":       gin.H{"from": since.Format("2006-01-02"), "to": now.Format("2006-01-02"), "days": days},
		"channels":     channels,
		"daily":        daily,
		"topReferrers": topReferrers,
		"previous":     gin.H{"channels": prevChannels},
	})
}

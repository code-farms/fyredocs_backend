package handlers

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"fyredocs/shared/response"

	"analytics-service/internal/models"
)

// adminActivitySortColumns is the closed set of ORDER BY targets. Sorting is
// interpolated into SQL, so the allowlist — not escaping — is the injection
// defense; an unknown sort silently falls back to occurred_at.
var adminActivitySortColumns = map[string]string{
	"occurredAt": "occurred_at",
	"createdAt":  "created_at",
	"eventType":  "event_type",
	"status":     "status",
	"platform":   "platform",
}

// AdminListActivity returns the full audit trail with filters, for the
// super-admin dashboard. Registered inside the adminAuth group — the handler
// itself does no role check.
func AdminListActivity(c *gin.Context) {
	limit := queryInt(c, "limit", 50)
	if limit > 100 {
		limit = 100
	}
	// Same deep-OFFSET rationale as GetEvents: beyond this depth the admin
	// needs a narrower filter, not a deeper page.
	page := queryInt(c, "page", 1)
	if page > maxEventsPage {
		page = maxEventsPage
	}
	offset := (page - 1) * limit

	query := rdb(c).Model(&models.UserActivity{})
	query = applyAdminActivityFilters(c, query)

	var total int64
	query.Count(&total)

	sortCol := adminActivitySortColumns[c.Query("sort")]
	if sortCol == "" {
		sortCol = "occurred_at"
	}
	order := "DESC"
	if strings.EqualFold(c.Query("order"), "asc") {
		order = "ASC"
	}

	var items []models.UserActivity
	query.Order(sortCol + " " + order).Limit(limit).Offset(offset).Find(&items)

	response.OKWithMeta(c, "Activity retrieved", items, &response.Meta{
		Page:  page,
		Limit: limit,
		Total: total,
	})
}

// applyAdminActivityFilters translates query params into WHERE clauses. All
// filters are exact-match except `search`, which ILIKEs the three columns an
// investigating admin actually greps for.
func applyAdminActivityFilters(c *gin.Context, query *gorm.DB) *gorm.DB {
	if v := strings.TrimSpace(c.Query("userId")); v != "" {
		if parsed, err := parseUUIDParam(v); err == nil {
			query = query.Where("user_id = ?", parsed)
		} else {
			// An unparseable userId must match nothing, not everything —
			// silently ignoring it would show an admin the full table while
			// they believe they are looking at one user.
			query = query.Where("1 = 0")
		}
	}
	for param, column := range map[string]string{
		"eventType":     "event_type",
		"featureId":     "feature_id",
		"toolId":        "tool_id",
		"documentId":    "document_id",
		"status":        "status",
		"platform":      "platform",
		"sessionId":     "session_id",
		"correlationId": "correlation_id",
	} {
		if v := c.Query(param); v != "" {
			query = query.Where(column+" = ?", v)
		}
	}
	if v := c.Query("search"); v != "" {
		pattern := "%" + v + "%"
		query = query.Where(
			"event_type ILIKE ? OR failure_reason ILIKE ? OR correlation_id ILIKE ?",
			pattern, pattern, pattern,
		)
	}
	if from, _, ok := parseTimeParam(c.Query("from")); ok {
		query = query.Where("occurred_at >= ?", from)
	}
	if to, dateOnly, ok := parseTimeParam(c.Query("to")); ok {
		if dateOnly {
			to = to.AddDate(0, 0, 1)
		}
		query = query.Where("occurred_at < ?", to)
	}
	return query
}

// parseUUIDParam wraps uuid.Parse with whitespace trimming; split out so the
// bad-userId fail-safe branch is testable without a database handle.
func parseUUIDParam(v string) (uuid.UUID, error) {
	return uuid.Parse(strings.TrimSpace(v))
}

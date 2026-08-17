package handlers

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"

	"fyredocs/shared/authverify"
	"fyredocs/shared/response"

	"analytics-service/internal/activity"
	"analytics-service/internal/models"
)

// exportRowCap bounds one export. Past this size the admin wants a database
// query, not a browser download — and an unbounded SELECT here is a trivial
// way to stall the service.
const exportRowCap = 10000

// AdminExportActivity streams the filtered audit trail as a CSV or JSON
// attachment. The export itself is audited FIRST — an admin.activity_exported
// row with the filters and row count is written before any data leaves, so
// "who exported what" can never be lost to a mid-stream disconnect.
func AdminExportActivity(c *gin.Context) {
	format := c.DefaultQuery("format", "csv")
	if format != "csv" && format != "json" {
		response.BadRequest(c, response.CodeInvalidInput, "format must be csv or json.")
		return
	}

	query := applyAdminActivityFilters(c, rdb(c).Model(&models.UserActivity{}))

	var items []models.UserActivity
	if err := query.Order("occurred_at DESC").Limit(exportRowCap).Find(&items).Error; err != nil {
		response.InternalErrorf(c, response.CodeServerError, "Export failed. Please try again.", err, "op", "activity.export")
		return
	}

	if err := recordExportAudit(c, format, len(items)); err != nil {
		// Fail closed: an unaudited export must not leave the building.
		response.InternalErrorf(c, response.CodeServerError, "Export failed. Please try again.", err, "op", "activity.export_audit")
		return
	}

	stamp := time.Now().UTC().Format("2006-01-02")
	if format == "json" {
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=activity-%s.json", stamp))
		c.JSON(http.StatusOK, items)
		return
	}

	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=activity-%s.csv", stamp))
	c.Header("Content-Type", "text/csv; charset=utf-8")
	w := csv.NewWriter(c.Writer)
	_ = w.Write([]string{
		"id", "occurredAt", "eventType", "status", "userId", "isGuest",
		"sessionId", "toolId", "documentId", "platform", "appVersion",
		"correlationId", "failureReason", "errorCode", "metadata",
	})
	for _, it := range items {
		userID := ""
		if it.UserID != nil {
			userID = it.UserID.String()
		}
		_ = w.Write([]string{
			it.ID.String(),
			it.OccurredAt.UTC().Format(time.RFC3339),
			it.EventType,
			it.Status,
			userID,
			strconv.FormatBool(it.IsGuest),
			it.SessionID,
			it.ToolID,
			it.DocumentID,
			it.Platform,
			it.AppVersion,
			it.CorrelationID,
			it.FailureReason,
			it.ErrorCode,
			string(it.Metadata),
		})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		slog.Warn("activity export stream interrupted", "error", err)
	}
}

// recordExportAudit writes the self-audit row for one export request.
func recordExportAudit(c *gin.Context, format string, rowCount int) error {
	authCtx, _ := authverify.GetGinAuth(c)
	var adminID *uuid.UUID
	if parsed, err := parseUUIDParam(authCtx.UserID); err == nil {
		adminID = &parsed
	}

	// The filters that scoped the export are the evidence that matters.
	filters := map[string]string{}
	for _, key := range []string{
		"userId", "eventType", "featureId", "toolId", "documentId", "status",
		"platform", "sessionId", "correlationId", "search", "from", "to",
	} {
		if v := c.Query(key); v != "" {
			filters[key] = v
		}
	}
	meta, err := json.Marshal(map[string]any{
		"filters":  filters,
		"rowCount": rowCount,
		"format":   format,
	})
	if err != nil {
		return err
	}

	row := models.UserActivity{
		UserID:     adminID,
		EventType:  activity.EventAdminActivityExported,
		Status:     activity.StatusSuccess,
		Platform:   "server",
		Metadata:   datatypes.JSON(meta),
		OccurredAt: time.Now().UTC(),
	}
	return rdb(c).Create(&row).Error
}

// exportContentTypes documents the closed format set; the format-validation
// test asserts against it so a new format cannot ship without a content type.
var exportContentTypes = map[string]string{
	"csv":  "text/csv",
	"json": "application/json",
}

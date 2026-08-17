package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"

	"fyredocs/shared/authverify"
	"fyredocs/shared/response"

	"analytics-service/internal/activity"
	"analytics-service/internal/models"
)

// Ingest limits. The body cap is enforced with MaxBytesReader so an oversized
// payload is cut off during the read, not after it was buffered; the per-event
// metadata cap keeps one event from consuming the whole batch's budget.
const (
	maxBatchEvents   = 50
	maxBodyBytes     = 128 << 10
	maxMetadataBytes = 4 << 10
	maxValueBytes    = 1 << 10
	maxFieldLen      = 256
	maxReasonLen     = 1024
	// maxClockSkew bounds how far a client clock can place occurred_at from
	// server time. Outside the window the value is clamped, not rejected —
	// a wrong wall clock should not cost the user their audit trail.
	maxClockSkew = 24 * time.Hour
)

// activityEventInput is one client-submitted event. Identity fields are
// deliberately absent: userId/isGuest always come from the verified auth
// context, so a client cannot write another user's history.
type activityEventInput struct {
	ClientEventID string                     `json:"clientEventId"`
	EventType     string                     `json:"eventType"`
	SessionID     string                     `json:"sessionId"`
	FeatureID     string                     `json:"featureId"`
	ToolID        string                     `json:"toolId"`
	Action        string                     `json:"action"`
	EntityType    string                     `json:"entityType"`
	EntityID      string                     `json:"entityId"`
	DocumentID    string                     `json:"documentId"`
	Status        string                     `json:"status"`
	FailureReason string                     `json:"failureReason"`
	ErrorCode     string                     `json:"errorCode"`
	OldValue      json.RawMessage            `json:"oldValue"`
	NewValue      json.RawMessage            `json:"newValue"`
	Metadata      map[string]json.RawMessage `json:"metadata"`
	Platform      string                     `json:"platform"`
	DeviceType    string                     `json:"deviceType"`
	AppVersion    string                     `json:"appVersion"`
	CorrelationID string                     `json:"correlationId"`
	OccurredAt    time.Time                  `json:"occurredAt"`
}

type ingestRequest struct {
	Events []activityEventInput `json:"events"`
}

type rejectedEvent struct {
	Index  int    `json:"index"`
	Reason string `json:"reason"`
}

// clientPlatforms is what the ingest path accepts; "server" is reserved for
// subscriber-written rows so a client cannot forge a server-origin event.
var clientPlatforms = map[string]bool{"web": true, "ios": true, "android": true}

// IngestActivity accepts a batch of client activity events. Acceptance is
// per-event (partial accept): one malformed event must not discard the other
// 49, because the client deletes its whole outbox batch on a 200. Retried
// batches dedupe on client_event_id, and the response separates "accepted"
// from "deduplicated" so clients and tests can tell an insert from a replay.
func IngestActivity(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)

	var req ingestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, response.CodeInvalidInput, "Request body is invalid or too large.")
		return
	}
	if len(req.Events) == 0 {
		response.BadRequest(c, response.CodeInvalidInput, "events must contain at least one event.")
		return
	}
	if len(req.Events) > maxBatchEvents {
		response.BadRequest(c, response.CodeInvalidInput, "events exceeds the batch limit of 50.")
		return
	}

	authCtx, _ := authverify.GetGinAuth(c)
	var userID *uuid.UUID
	if parsed, err := uuid.Parse(strings.TrimSpace(authCtx.UserID)); err == nil {
		userID = &parsed
	}
	// Anonymous callers are recorded as guests: guest tool usage is a real
	// audit subject, and rejecting it would push clients to buffer forever.
	isGuest := authCtx.IsGuest || userID == nil

	now := time.Now().UTC()
	rows := make([]models.UserActivity, 0, len(req.Events))
	rejected := make([]rejectedEvent, 0)

	for i, in := range req.Events {
		if reason := validateEvent(&in); reason != "" {
			rejected = append(rejected, rejectedEvent{Index: i, Reason: reason})
			continue
		}

		occurred := in.OccurredAt.UTC()
		if occurred.IsZero() {
			occurred = now
		} else if occurred.Before(now.Add(-maxClockSkew)) {
			occurred = now.Add(-maxClockSkew)
		} else if occurred.After(now.Add(maxClockSkew)) {
			occurred = now
		}

		clientEventID := strings.TrimSpace(in.ClientEventID)
		status := in.Status
		if status == "" {
			status = activity.StatusSuccess
		}

		rows = append(rows, models.UserActivity{
			ClientEventID: &clientEventID,
			UserID:        userID,
			IsGuest:       isGuest,
			SessionID:     in.SessionID,
			EventType:     in.EventType,
			FeatureID:     in.FeatureID,
			ToolID:        in.ToolID,
			Action:        in.Action,
			EntityType:    in.EntityType,
			EntityID:      in.EntityID,
			DocumentID:    in.DocumentID,
			Status:        status,
			FailureReason: truncate(in.FailureReason, maxReasonLen),
			ErrorCode:     in.ErrorCode,
			OldValue:      boundedJSON(in.OldValue),
			NewValue:      boundedJSON(in.NewValue),
			Metadata:      sanitizeMetadata(in.Metadata),
			Platform:      in.Platform,
			DeviceType:    in.DeviceType,
			AppVersion:    in.AppVersion,
			CorrelationID: in.CorrelationID,
			OccurredAt:    occurred,
		})
	}

	inserted, err := models.InsertUserActivities(rdb(c), rows)
	if err != nil {
		response.InternalErrorf(c, response.CodeServerError, "Could not record activity. Please try again.", err, "op", "activity.ingest")
		return
	}

	response.OK(c, "events recorded", gin.H{
		"accepted":     inserted,
		"deduplicated": int64(len(rows)) - inserted,
		"rejected":     rejected,
	})
}

// validateEvent returns a rejection reason, or "" when the event is storable.
func validateEvent(in *activityEventInput) string {
	if strings.TrimSpace(in.ClientEventID) == "" {
		return "clientEventId is required"
	}
	if len(in.ClientEventID) > maxFieldLen {
		return "clientEventId too long"
	}
	if !activity.ValidEventType(in.EventType) {
		return "eventType is not in the allowed catalog"
	}
	if in.Status != "" && !activity.ValidStatus(in.Status) {
		return "status must be started|success|failed|cancelled"
	}
	if !clientPlatforms[in.Platform] {
		return "platform must be web|ios|android"
	}
	for _, f := range []string{
		in.SessionID, in.FeatureID, in.ToolID, in.Action, in.EntityType,
		in.EntityID, in.DocumentID, in.ErrorCode, in.DeviceType,
		in.AppVersion, in.CorrelationID,
	} {
		if len(f) > maxFieldLen {
			return "field exceeds 256 characters"
		}
	}
	if metadataSize(in.Metadata) > maxMetadataBytes {
		return "metadata exceeds 4KB"
	}
	return ""
}

// sanitizeMetadata strips sensitive keys and re-marshals. Stripping happens
// server-side even though SDKs whitelist keys — the server cannot assume every
// future client builds metadata correctly.
func sanitizeMetadata(meta map[string]json.RawMessage) datatypes.JSON {
	if len(meta) == 0 {
		return nil
	}
	clean := make(map[string]json.RawMessage, len(meta))
	for k, v := range meta {
		if activity.SensitiveMetadataKey(k) {
			continue
		}
		clean[k] = v
	}
	if len(clean) == 0 {
		return nil
	}
	out, err := json.Marshal(clean)
	if err != nil {
		return nil
	}
	return datatypes.JSON(out)
}

// boundedJSON caps old/new value payloads: they exist for settings scalars,
// so anything beyond 1KB is dropped rather than stored.
func boundedJSON(raw json.RawMessage) datatypes.JSON {
	if len(raw) == 0 || len(raw) > maxValueBytes || !json.Valid(raw) {
		return nil
	}
	return datatypes.JSON(raw)
}

func metadataSize(meta map[string]json.RawMessage) int {
	size := 0
	for k, v := range meta {
		size += len(k) + len(v)
	}
	return size
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// GetMyActivity returns the authenticated caller's own activity feed, newest
// first. Guests get 401 — their events exist but carry no user_id, so there
// is nothing safe to scope a personal feed to.
func GetMyActivity(c *gin.Context) {
	authCtx, _ := authverify.GetGinAuth(c)
	if !authverify.IsAuthenticated(authCtx) {
		response.Err(c, http.StatusUnauthorized, response.CodeUnauthorized, "Please log in to view your activity.")
		return
	}
	userID, err := uuid.Parse(strings.TrimSpace(authCtx.UserID))
	if err != nil {
		response.Err(c, http.StatusUnauthorized, response.CodeUnauthorized, "Please log in to view your activity.")
		return
	}

	limit := queryInt(c, "limit", 50)
	if limit > 200 {
		limit = 200
	}
	// Same deep-OFFSET cap rationale as GetEvents: past this depth the caller
	// needs a narrower filter, not a deeper page.
	page := queryInt(c, "page", 1)
	if page > maxEventsPage {
		page = maxEventsPage
	}
	offset := (page - 1) * limit

	query := rdb(c).Model(&models.UserActivity{}).Where("user_id = ?", userID)
	if v := c.Query("eventType"); v != "" {
		query = query.Where("event_type = ?", v)
	}
	if v := c.Query("status"); v != "" {
		query = query.Where("status = ?", v)
	}
	if from, _, ok := parseTimeParam(c.Query("from")); ok {
		query = query.Where("occurred_at >= ?", from)
	}
	if to, dateOnly, ok := parseTimeParam(c.Query("to")); ok {
		// A bare date as a range end means "through that whole day".
		if dateOnly {
			to = to.Add(24 * time.Hour)
		}
		query = query.Where("occurred_at < ?", to)
	}

	var total int64
	query.Count(&total)

	var items []models.UserActivity
	query.Order("occurred_at DESC").Limit(limit).Offset(offset).Find(&items)

	response.OKWithMeta(c, "Activity retrieved", items, &response.Meta{
		Page:  page,
		Limit: limit,
		Total: total,
	})
}

// parseTimeParam accepts RFC3339 or a bare date; dateOnly tells the caller
// which form arrived so a bare end date can cover its whole day.
func parseTimeParam(v string) (t time.Time, dateOnly bool, ok bool) {
	if v == "" {
		return time.Time{}, false, false
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, false, true
	}
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return t, true, true
	}
	return time.Time{}, false, false
}

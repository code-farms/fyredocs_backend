package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"fyredocs/shared/logger"
	"fyredocs/shared/natsconn"
	"fyredocs/shared/response"

	"notification-service/internal/models"
)

// createAndPush persists a notification and fans it out to live SSE clients. The
// live push is best-effort (the durable row is the source of truth), but a push
// failure is logged rather than silently dropped.
func createAndPush(ctx context.Context, n *models.Notification) error {
	if err := models.DB.Create(n).Error; err != nil {
		return err
	}
	if natsconn.Conn != nil {
		data, err := json.Marshal(n)
		if err != nil {
			logger.LogWarn(ctx, "notify.marshal_push", err, "userId", n.UserID)
			return nil
		}
		if err := natsconn.Conn.Publish("notify."+n.UserID.String(), data); err != nil {
			logger.LogWarn(ctx, "nats.publish_notify", err, "userId", n.UserID)
		}
	}
	return nil
}

type internalNotifReq struct {
	UserID   string `json:"userId"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Link     string `json:"link"`
	SourceID string `json:"sourceId"`
}

// CreateInternal lets other services raise a notification. It is mounted on an
// /internal path that the gateway does not proxy, so it is mesh-only.
// Idempotent when sourceId is supplied.
func CreateInternal(c *gin.Context) {
	var req internalNotifReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_BODY", "Invalid request body.")
		return
	}
	uid, err := uuid.Parse(strings.TrimSpace(req.UserID))
	if err != nil {
		response.BadRequest(c, "INVALID_USER", "userId must be a valid UUID.")
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		response.BadRequest(c, "INVALID_TITLE", "title is required.")
		return
	}

	var src *uuid.UUID
	if s := strings.TrimSpace(req.SourceID); s != "" {
		if parsed, err := uuid.Parse(s); err == nil {
			src = &parsed
			var count int64
			if err := models.DB.Model(&models.Notification{}).Where("user_id = ? AND source_job_id = ?", uid, parsed).Count(&count).Error; err != nil {
				logger.LogWarn(c.Request.Context(), "db.notifications.dedup_count", err, "userId", uid)
			}
			if count > 0 {
				response.OK(c, "Notification already exists", gin.H{})
				return
			}
		}
	}

	ntype := strings.TrimSpace(req.Type)
	if ntype == "" {
		ntype = "info"
	}
	n := models.Notification{UserID: uid, Type: ntype, Title: req.Title, Body: req.Body, Link: req.Link, SourceJobID: src}
	if err := createAndPush(c.Request.Context(), &n); err != nil {
		response.InternalErrorf(c, "CREATE_FAILED", "Could not create notification.", err,
			"op", "db.notifications.create", "userId", uid)
		return
	}
	response.Created(c, "Notification created", n)
}

func userID(c *gin.Context) (uuid.UUID, bool) {
	raw := strings.TrimSpace(c.GetHeader("X-User-ID"))
	if raw == "" {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// RequireUser aborts unauthenticated requests.
func RequireUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := userID(c); !ok {
			response.Err(c, http.StatusUnauthorized, "UNAUTHORIZED", "Please log in.")
			c.Abort()
			return
		}
		c.Next()
	}
}

// ListNotifications returns the caller's recent notifications + unread count.
func ListNotifications(c *gin.Context) {
	uid, _ := userID(c)

	// The page fetch and the unread count are independent reads against a remote
	// DB; run them concurrently to collapse two sequential round-trips into one.
	var (
		items   []models.Notification
		unread  int64
		listErr error
		wg      sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		listErr = models.DB.Where("user_id = ?", uid).Order("created_at DESC").Limit(50).Find(&items).Error
	}()
	var unreadErr error
	go func() {
		defer wg.Done()
		unreadErr = models.DB.Model(&models.Notification{}).Where("user_id = ? AND read_at IS NULL", uid).Count(&unread).Error
	}()
	wg.Wait()
	if listErr != nil {
		response.InternalErrorf(c, "LIST_FAILED", "Could not load notifications.", listErr,
			"op", "db.notifications.list", "userId", uid)
		return
	}
	if unreadErr != nil {
		logger.LogWarn(c.Request.Context(), "db.notifications.count_unread", unreadErr, "userId", uid)
	}

	response.OK(c, "Notifications retrieved", gin.H{"notifications": items, "unreadCount": unread})
}

// MarkRead marks one notification read.
func MarkRead(c *gin.Context) {
	uid, _ := userID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid notification id.")
		return
	}
	now := time.Now().UTC()
	res := models.DB.Model(&models.Notification{}).
		Where("id = ? AND user_id = ? AND read_at IS NULL", id, uid).
		Update("read_at", now)
	if res.Error != nil {
		response.InternalErrorf(c, "UPDATE_FAILED", "Could not update notification.", res.Error,
			"op", "db.notifications.mark_read", "notificationId", id, "userId", uid)
		return
	}
	response.OK(c, "Notification marked read", gin.H{"id": id})
}

// StreamNotifications is a Server-Sent Events endpoint that pushes new
// notifications to the caller live. It subscribes to the user's core-NATS
// fan-out subject; the durable copy is fetched via ListNotifications.
func StreamNotifications(c *gin.Context) {
	uid, _ := userID(c)
	if natsconn.Conn == nil {
		slog.WarnContext(c.Request.Context(), "SSE requested but NATS is unavailable", "op", "nats.sse_unavailable", "userId", uid)
		response.Err(c, http.StatusServiceUnavailable, response.CodeServiceUnavailable, "Live updates are temporarily unavailable.")
		return
	}

	w := c.Writer
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering
	flusher, ok := w.(http.Flusher)
	if !ok {
		response.InternalError(c, "STREAM_UNSUPPORTED", "Streaming is not supported.")
		return
	}

	msgCh := make(chan *nats.Msg, 16)
	sub, err := natsconn.Conn.ChanSubscribe("notify."+uid.String(), msgCh)
	if err != nil {
		response.InternalErrorf(c, "SUBSCRIBE_FAILED", "Could not open the live stream.", err,
			"op", "nats.chan_subscribe", "userId", uid)
		return
	}
	defer func() { _ = sub.Unsubscribe() }()

	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	ctx := c.Request.Context()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case m := <-msgCh:
			fmt.Fprintf(w, "event: notification\ndata: %s\n\n", m.Data)
			flusher.Flush()
		}
	}
}

// MarkAllRead marks all of the caller's notifications read.
func MarkAllRead(c *gin.Context) {
	uid, _ := userID(c)
	now := time.Now().UTC()
	if err := models.DB.Model(&models.Notification{}).
		Where("user_id = ? AND read_at IS NULL", uid).
		Update("read_at", now).Error; err != nil {
		response.InternalErrorf(c, "UPDATE_FAILED", "Could not update notifications.", err,
			"op", "db.notifications.mark_all", "userId", uid)
		return
	}
	response.OK(c, "All notifications marked read", gin.H{})
}

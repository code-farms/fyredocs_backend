package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"fyredocs/shared/natsconn"
	"fyredocs/shared/response"

	"notification-service/internal/models"
)

// createAndPush persists a notification and fans it out to live SSE clients.
func createAndPush(n *models.Notification) error {
	if err := models.DB.Create(n).Error; err != nil {
		return err
	}
	if natsconn.Conn != nil {
		if data, err := json.Marshal(n); err == nil {
			_ = natsconn.Conn.Publish("notify."+n.UserID.String(), data)
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
			models.DB.Model(&models.Notification{}).Where("user_id = ? AND source_job_id = ?", uid, parsed).Count(&count)
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
	if err := createAndPush(&n); err != nil {
		response.InternalError(c, "CREATE_FAILED", "Could not create notification.")
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
	var items []models.Notification
	if err := models.DB.Where("user_id = ?", uid).Order("created_at DESC").Limit(50).Find(&items).Error; err != nil {
		response.InternalError(c, "LIST_FAILED", "Could not load notifications.")
		return
	}
	var unread int64
	models.DB.Model(&models.Notification{}).Where("user_id = ? AND read_at IS NULL", uid).Count(&unread)

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
		response.InternalError(c, "UPDATE_FAILED", "Could not update notification.")
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
		response.InternalError(c, "NATS_UNAVAILABLE", "Live updates are unavailable.")
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
		response.InternalError(c, "SUBSCRIBE_FAILED", "Could not open the live stream.")
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
		response.InternalError(c, "UPDATE_FAILED", "Could not update notifications.")
		return
	}
	response.OK(c, "All notifications marked read", gin.H{})
}

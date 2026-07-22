// Package notifyclient raises in-app notifications via notification-service.
// Best-effort: failures never block the calling flow.
package notifyclient

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"fyredocs/shared/logger"
)

func baseURL() string {
	if v := strings.TrimSpace(os.Getenv("NOTIFICATION_SERVICE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://notification-service:8091"
}

var httpClient = &http.Client{Timeout: 4 * time.Second}

type notifyReq struct {
	UserID   string `json:"userId"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Link     string `json:"link"`
	SourceID string `json:"sourceId"`
}

// Notify posts a notification request. Errors are swallowed (best-effort).
func Notify(ctx context.Context, userID, ntype, title, body, link, sourceID string) {
	payload, err := json.Marshal(notifyReq{UserID: userID, Type: ntype, Title: title, Body: body, Link: link, SourceID: sourceID})
	if err != nil {
		logger.LogWarn(ctx, "notify.marshal", err, "userId", userID, "type", ntype)
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL()+"/internal/notifications", bytes.NewReader(payload))
	if err != nil {
		logger.LogWarn(ctx, "notify.build_request", err, "userId", userID, "type", ntype)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		logger.LogWarn(ctx, "notify.post", err, "userId", userID, "type", ntype)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		slog.WarnContext(ctx, "notify non-2xx response", "op", "notify.post", "userId", userID, "type", ntype, "status", resp.StatusCode)
	}
}

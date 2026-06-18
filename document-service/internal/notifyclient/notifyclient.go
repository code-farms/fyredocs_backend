// Package notifyclient raises in-app notifications via notification-service.
// Best-effort: failures never block the calling flow.
package notifyclient

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"
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
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL()+"/internal/notifications", bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

// Package notifyclient raises in-app notifications via notification-service.
// Best-effort: failures never block the calling flow.
package notifyclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"fyredocs/shared/circuitbreaker"
	"fyredocs/shared/logger"
)

// breaker fails fast when notification-service is down so best-effort notifies
// don't each block on the full HTTP timeout.
var breaker = circuitbreaker.New[*http.Response]("notification-service.notify")

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
	// Breaker: once notification-service is repeatedly failing, skip the POST and
	// fail fast instead of every caller waiting the full 4s timeout (best-effort,
	// so a skipped notification is acceptable).
	resp, err := breaker.Execute(func() (*http.Response, error) {
		r, derr := httpClient.Do(req)
		if derr != nil {
			return nil, derr
		}
		if r.StatusCode >= 500 {
			r.Body.Close()
			return nil, fmt.Errorf("notify status %d", r.StatusCode)
		}
		return r, nil
	})
	if err != nil {
		logger.LogWarn(ctx, "notify.post", err, "userId", userID, "type", ntype)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		slog.WarnContext(ctx, "notify non-2xx response", "op", "notify.post", "userId", userID, "type", ntype, "status", resp.StatusCode)
	}
}

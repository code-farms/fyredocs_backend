// Package discord delivers alert notifications to a Discord channel webhook and
// provides an HTTP receiver that speaks the Prometheus/Alertmanager v2 alert API
// (POST .../api/v2/alerts). Mounting the receiver in an existing service lets
// Prometheus deliver alerts to Discord WITHOUT running a separate Alertmanager
// container. It reads DISCORD_WEBHOOK_URL (empty = disabled/no-op) and
// DISCORD_ALERT_REPEAT (re-notify interval for a still-firing alert, default 4h).
package discord

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"fyredocs/shared/circuitbreaker"
	"fyredocs/shared/config"
	"fyredocs/shared/logger"
	"fyredocs/shared/response"
)

// Embed colors (decimal RGB) by alert state.
const (
	colorCritical = 0xE01E5A // red
	colorWarning  = 0xE8912D // orange
	colorResolved = 0x2EB67D // green
)

// Embed is one Discord message embed.
type Embed struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Color       int    `json:"color"`
}

// Message is the native Discord webhook payload.
type Message struct {
	Content string  `json:"content,omitempty"`
	Embeds  []Embed `json:"embeds,omitempty"`
}

// Alert is one entry of the Prometheus -> Alertmanager v2 POST body (a bare
// JSON array). Firing vs resolved is derived from EndsAt.
type Alert struct {
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
}

// IsResolved reports whether the alert has cleared: Alertmanager v2 sets EndsAt
// to a past time on resolution; a zero or future EndsAt means still firing.
func (a Alert) IsResolved(now time.Time) bool {
	return !a.EndsAt.IsZero() && a.EndsAt.Before(now)
}

// fingerprint is a stable id for an alert across re-sends: the sorted label set.
func (a Alert) fingerprint() string {
	keys := make([]string, 0, len(a.Labels))
	for k := range a.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(a.Labels[k])
		b.WriteByte(0)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// throttle replaces the dedup/repeat Alertmanager used to do: Prometheus re-POSTs
// firing alerts every evaluation interval, so without this Discord would be
// spammed. It re-notifies only on a state change (firing<->resolved) or after
// `repeat` has elapsed for a still-firing alert. In-memory (single-instance host).
type throttle struct {
	mu     sync.Mutex
	repeat time.Duration
	seen   map[string]entry
}

type entry struct {
	firing bool
	last   time.Time
}

func newThrottle(repeat time.Duration) *throttle {
	return &throttle{repeat: repeat, seen: map[string]entry{}}
}

// shouldNotify decides whether to send, and records the decision.
func (t *throttle) shouldNotify(fp string, firing bool, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	prev, ok := t.seen[fp]

	// Resolved: notify once when transitioning from firing; then forget it.
	if !firing {
		if ok && prev.firing {
			delete(t.seen, fp)
			return true
		}
		// Never-seen or already-resolved: nothing to say.
		if ok {
			delete(t.seen, fp)
		}
		return false
	}

	// Firing: notify on first sight, on firing<-resolved change, or after repeat.
	if !ok || !prev.firing || now.Sub(prev.last) >= t.repeat {
		t.seen[fp] = entry{firing: true, last: now}
		return true
	}
	return false
}

// Client posts to a Discord webhook. A zero/empty webhook URL disables sending
// (Send is a no-op) so the receiver works even when Discord isn't configured.
type Client struct {
	webhookURL string
	http       *http.Client
	breaker    *circuitbreaker.Breaker[*http.Response]
	throttle   *throttle
}

// NewFromEnv builds a Client from DISCORD_WEBHOOK_URL / DISCORD_ALERT_REPEAT.
func NewFromEnv() *Client {
	return &Client{
		webhookURL: strings.TrimSpace(config.GetEnv("DISCORD_WEBHOOK_URL", "")),
		http:       &http.Client{Timeout: 5 * time.Second},
		breaker:    circuitbreaker.New[*http.Response]("discord.webhook"),
		throttle:   newThrottle(config.GetEnvDuration("DISCORD_ALERT_REPEAT", 4*time.Hour)),
	}
}

// Enabled reports whether a webhook URL is configured.
func (c *Client) Enabled() bool { return c.webhookURL != "" }

// Send posts a message to the Discord webhook. No-op (nil) when disabled. 5xx
// trips the breaker so a Discord outage fails fast instead of blocking.
func (c *Client) Send(ctx context.Context, msg Message) error {
	if !c.Enabled() {
		return nil
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.webhookURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.breaker.Execute(func() (*http.Response, error) {
		r, derr := c.http.Do(req)
		if derr != nil {
			return nil, derr
		}
		if r.StatusCode >= 500 {
			r.Body.Close()
			return nil, fmt.Errorf("discord webhook status %d", r.StatusCode)
		}
		return r, nil
	})
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// embedFor renders one alert as a Discord embed.
func embedFor(a Alert, resolved bool) Embed {
	name := a.Labels["alertname"]
	if name == "" {
		name = "alert"
	}
	sev := a.Labels["severity"]
	prefix := "🔥 FIRING"
	color := colorWarning
	if resolved {
		prefix, color = "✅ RESOLVED", colorResolved
	} else if sev == "critical" {
		color = colorCritical
	}

	title := fmt.Sprintf("%s: %s", prefix, name)
	if sev != "" {
		title += " (" + sev + ")"
	}

	var b strings.Builder
	if s := a.Annotations["summary"]; s != "" {
		b.WriteString(s)
		b.WriteByte('\n')
	}
	if d := a.Annotations["description"]; d != "" {
		b.WriteString(d)
		b.WriteByte('\n')
	}
	if job := a.Labels["job"]; job != "" {
		b.WriteString("`job=" + job)
		if inst := a.Labels["instance"]; inst != "" {
			b.WriteString(" instance=" + inst)
		}
		b.WriteString("`")
	}
	desc := strings.TrimSpace(b.String())
	if desc == "" {
		desc = "(no details)"
	}
	return Embed{Title: title, Description: desc, Color: color}
}

// AlertWebhookHandler returns a Gin handler that accepts the Prometheus ->
// Alertmanager v2 alert POST (a bare []Alert), throttles re-sends, and forwards
// surviving alerts to Discord. It always answers 200 (Prometheus retries on
// error); Discord failures are logged, not surfaced.
func AlertWebhookHandler(c *Client) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var alerts []Alert
		if err := ctx.ShouldBindJSON(&alerts); err != nil {
			response.BadRequest(ctx, "INVALID_INPUT", "Invalid alert payload.")
			return
		}
		now := time.Now()
		sent := 0
		for _, a := range alerts {
			resolved := a.IsResolved(now)
			if !c.throttle.shouldNotify(a.fingerprint(), !resolved, now) {
				continue
			}
			if err := c.Send(ctx.Request.Context(), Message{Embeds: []Embed{embedFor(a, resolved)}}); err != nil {
				logger.LogWarn(ctx.Request.Context(), "discord.send", err,
					"alertname", a.Labels["alertname"], "resolved", resolved)
				continue
			}
			sent++
		}
		response.OK(ctx, "alerts processed", gin.H{"received": len(alerts), "notified": sent})
	}
}

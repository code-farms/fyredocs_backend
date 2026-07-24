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
	"unicode"

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
type EmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

type EmbedAuthor struct {
	Name    string `json:"name"`
	IconURL string `json:"icon_url,omitempty"`
}

type EmbedFooter struct {
	Text    string `json:"text"`
	IconURL string `json:"icon_url,omitempty"`
}

type Embed struct {
	Title       string       `json:"title,omitempty"`
	Description string       `json:"description,omitempty"`
	URL         string       `json:"url,omitempty"`
	Color       int          `json:"color"`
	Author      *EmbedAuthor `json:"author,omitempty"`
	Fields      []EmbedField `json:"fields,omitempty"`
	Footer      *EmbedFooter `json:"footer,omitempty"`
	Timestamp   string       `json:"timestamp,omitempty"` // ISO-8601 (RFC3339); Discord renders it in the footer
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
	webhookURL   string
	iconURL      string // optional brand logo (author/footer); renders only from a public HTTPS URL
	dashboardURL string // optional Grafana link; makes the embed title clickable
	environment  string // optional footer badge, e.g. "production"
	http         *http.Client
	breaker      *circuitbreaker.Breaker[*http.Response]
	throttle     *throttle
}

// NewFromEnv builds a Client from DISCORD_WEBHOOK_URL / DISCORD_ALERT_REPEAT plus
// the embed-branding vars DISCORD_ALERT_ICON_URL / DISCORD_DASHBOARD_URL /
// ENVIRONMENT (all optional).
func NewFromEnv() *Client {
	return &Client{
		webhookURL:   strings.TrimSpace(config.GetEnv("DISCORD_WEBHOOK_URL", "")),
		iconURL:      strings.TrimSpace(config.GetEnv("DISCORD_ALERT_ICON_URL", "")),
		dashboardURL: strings.TrimSpace(config.GetEnv("DISCORD_DASHBOARD_URL", "")),
		environment:  strings.TrimSpace(config.GetEnv("ENVIRONMENT", config.GetEnv("APP_ENV", ""))),
		http:         &http.Client{Timeout: 5 * time.Second},
		breaker:      circuitbreaker.New[*http.Response]("discord.webhook"),
		throttle:     newThrottle(config.GetEnvDuration("DISCORD_ALERT_REPEAT", 4*time.Hour)),
	}
}

// footerText is the consistent footer used across all embeds.
func (c *Client) footerText() string {
	if c.environment != "" {
		return "Fyredocs • " + c.environment
	}
	return "Fyredocs"
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

// humanizeAlertName turns a CamelCase rule name into spaced words, e.g.
// "ServiceDown" → "Service Down", "DBPoolNearExhaustion" → "DB Pool Near
// Exhaustion", "DLQBacklog" → "DLQ Backlog".
func humanizeAlertName(s string) string {
	if s == "" {
		return "Alert"
	}
	rs := []rune(s)
	var b strings.Builder
	for i, r := range rs {
		if i > 0 && unicode.IsUpper(r) {
			prev := rs[i-1]
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
			// space before an uppercase that starts a new word: after a lowercase,
			// or at the tail of an ACRONYM before a Capitalized word.
			if unicode.IsLower(prev) || (unicode.IsUpper(prev) && nextLower) {
				b.WriteByte(' ')
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	rs := []rune(s)
	rs[0] = unicode.ToUpper(rs[0])
	return string(rs)
}

// embedFor renders one alert as the executive rich-card Discord embed: brand
// author (+logo), severity-colored side bar, clickable title, summary, inline
// Status/Severity/Service fields, a Details block, and a footer + timestamp.
func (c *Client) embedFor(a Alert, resolved bool) Embed {
	sev := a.Labels["severity"]

	// Side-bar color + title emoji by state/severity.
	color := colorWarning
	titleEmoji := "🟠"
	if resolved {
		color, titleEmoji = colorResolved, "✅"
	} else if sev == "critical" {
		color, titleEmoji = colorCritical, "🔴"
	}

	statusEmoji, statusText := "🔥", "Firing"
	if resolved {
		statusEmoji, statusText = "✅", "Resolved"
	}

	sevLabel := "—"
	if sev != "" {
		sevLabel = capitalizeFirst(sev)
	}

	service := a.Labels["job"]
	if inst := a.Labels["instance"]; inst != "" {
		if service != "" {
			service += " · " + inst
		} else {
			service = inst
		}
	}
	if service == "" {
		service = "—"
	}

	summary := strings.TrimSpace(a.Annotations["summary"])
	if summary == "" {
		summary = "(no summary provided)"
	}

	// Timestamp: when the alert cleared (resolved) or started (firing).
	ts := a.StartsAt
	if resolved && !a.EndsAt.IsZero() {
		ts = a.EndsAt
	}

	fields := []EmbedField{
		{Name: "Status", Value: statusEmoji + " " + statusText, Inline: true},
		{Name: "Severity", Value: sevLabel, Inline: true},
		{Name: "Service", Value: "`" + service + "`", Inline: true},
	}
	// A live relative time ("12 minutes ago") via Discord's <t:unix:R> markdown —
	// at-a-glance urgency. "Since" while firing, "Cleared" once resolved.
	if !ts.IsZero() {
		sinceLabel := "Since"
		if resolved {
			sinceLabel = "Cleared"
		}
		fields = append(fields, EmbedField{Name: sinceLabel, Value: fmt.Sprintf("<t:%d:R>", ts.Unix()), Inline: true})
	}
	// Details only when the description adds something beyond the summary.
	if d := strings.TrimSpace(a.Annotations["description"]); d != "" && d != summary {
		fields = append(fields, EmbedField{Name: "Details", Value: d, Inline: false})
	}

	embed := Embed{
		Title:       titleEmoji + " " + humanizeAlertName(a.Labels["alertname"]),
		Description: summary,
		Color:       color,
		Author:      &EmbedAuthor{Name: "Fyredocs Monitoring"},
		Fields:      fields,
		Footer:      &EmbedFooter{Text: c.footerText()},
	}

	// Clickable title → Grafana dashboard (or the alert's Prometheus link).
	if c.dashboardURL != "" {
		embed.URL = c.dashboardURL
	} else if a.GeneratorURL != "" {
		embed.URL = a.GeneratorURL
	}

	// Brand logo on author + footer (only renders from a public HTTPS URL).
	if c.iconURL != "" {
		embed.Author.IconURL = c.iconURL
		embed.Footer.IconURL = c.iconURL
	}

	// Absolute embed timestamp (Discord renders it in the footer).
	if !ts.IsZero() {
		embed.Timestamp = ts.UTC().Format(time.RFC3339)
	}

	return embed
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
			if err := c.Send(ctx.Request.Context(), Message{Embeds: []Embed{c.embedFor(a, resolved)}}); err != nil {
				logger.LogWarn(ctx.Request.Context(), "discord.send", err,
					"alertname", a.Labels["alertname"], "resolved", resolved)
				continue
			}
			sent++
		}
		response.OK(ctx, "alerts processed", gin.H{"received": len(alerts), "notified": sent})
	}
}

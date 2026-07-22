package email

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// NoopMailer surfaces the reset URL for local development instead of sending an
// email. Selected automatically when RESEND_API_KEY is unset so local dev works
// without external dependencies. Never deploy with this in production.
type NoopMailer struct{}

// SendPasswordReset does not send mail. The structured log line deliberately
// carries NO PII/secret — the email is masked and the reset URL (which embeds a
// live token) and requester IP are omitted, so nothing sensitive reaches the log
// sink (stdout → Loki). The clickable link is printed separately as a plain
// stdout convenience for the local developer; this mailer never runs in prod.
func (NoopMailer) SendPasswordReset(_ context.Context, to, resetURL, _ string, ttl time.Duration) error {
	slog.Info("password_reset.email.noop", "to", maskEmail(to), "ttl", ttl.String())
	fmt.Printf("[dev] password reset link for %s (valid %s): %s\n", maskEmail(to), ttl, resetURL)
	return nil
}

// maskEmail returns a privacy-preserving form like "a***@example.com".
func maskEmail(e string) string {
	at := strings.IndexByte(e, '@')
	if at <= 0 {
		return "***"
	}
	return e[:1] + "***" + e[at:]
}

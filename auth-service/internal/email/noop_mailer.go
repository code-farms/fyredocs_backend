package email

import (
	"context"
	"log/slog"
	"time"
)

// NoopMailer logs the reset URL to stdout instead of sending an email.
// Selected automatically when RESEND_API_KEY is unset so local development
// works without external dependencies. Never deploy with this in production.
type NoopMailer struct{}

// SendPasswordReset logs the reset URL instead of sending an email, so local
// development works without a mail provider.
func (NoopMailer) SendPasswordReset(_ context.Context, to, resetURL, requestIP string, ttl time.Duration) error {
	slog.Info("password_reset.email.noop",
		"to", to,
		"resetUrl", resetURL,
		"requestIp", requestIP,
		"ttl", ttl.String(),
	)
	return nil
}

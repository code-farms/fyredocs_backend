package email

import (
	"context"
	"errors"
	"fmt"
	"html"
	"time"

	"github.com/resend/resend-go/v2"

	"fyredocs/shared/config"
)

// ResendMailer sends transactional email via the Resend HTTPS API.
type ResendMailer struct {
	client *resend.Client
	from   string
}

// NewResendMailerFromEnv constructs a ResendMailer from RESEND_API_KEY and
// RESET_EMAIL_FROM. The From address must use a domain verified in the
// Resend dashboard, otherwise Resend will only deliver to the account owner.
func NewResendMailerFromEnv() (*ResendMailer, error) {
	apiKey := config.GetEnv("RESEND_API_KEY", "")
	if apiKey == "" {
		return nil, errors.New("RESEND_API_KEY is not set")
	}
	from := config.GetEnv("RESET_EMAIL_FROM", "")
	if from == "" {
		return nil, errors.New("RESET_EMAIL_FROM is not set")
	}
	return &ResendMailer{
		client: resend.NewClient(apiKey),
		from:   from,
	}, nil
}

func (m *ResendMailer) SendPasswordReset(ctx context.Context, to, resetURL, requestIP string, ttl time.Duration) error {
	minutes := int(ttl.Minutes())
	if minutes < 1 {
		minutes = 1
	}
	now := time.Now().UTC().Format("2006-01-02 15:04 UTC")

	safeURL := html.EscapeString(resetURL)
	safeIP := html.EscapeString(requestIP)

	htmlBody := fmt.Sprintf(`<!doctype html>
<html><body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; color: #111; max-width: 560px; margin: 0 auto; padding: 24px;">
  <h2 style="margin: 0 0 16px;">Reset your fyredocs password</h2>
  <p>Someone requested a password reset for your account at %s from IP <code>%s</code>.</p>
  <p>If this was you, click the button below within %d minutes to choose a new password.</p>
  <p style="margin: 32px 0;">
    <a href="%s" style="background:#111;color:#fff;text-decoration:none;padding:12px 20px;border-radius:6px;display:inline-block;">Reset password</a>
  </p>
  <p style="font-size:13px;color:#555;">Or copy this link into your browser:<br><a href="%s">%s</a></p>
  <hr style="border:none;border-top:1px solid #eee;margin:32px 0;">
  <p style="font-size:12px;color:#888;">If you didn't request this, you can safely ignore the email — your password will stay the same.</p>
</body></html>`, now, safeIP, minutes, safeURL, safeURL, safeURL)

	textBody := fmt.Sprintf(`Reset your fyredocs password

Someone requested a password reset for your account at %s from IP %s.
If this was you, open the link below within %d minutes to choose a new password:

%s

If you didn't request this, you can safely ignore this email — your password will stay the same.
`, now, requestIP, minutes, resetURL)

	params := &resend.SendEmailRequest{
		From:    m.from,
		To:      []string{to},
		Subject: "Reset your fyredocs password",
		Html:    htmlBody,
		Text:    textBody,
	}
	_, err := m.client.Emails.SendWithContext(ctx, params)
	return err
}

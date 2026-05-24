// Package email provides a minimal mail-sending interface for auth-service.
// It is intentionally service-private (not under shared/) to honour the
// microservice boundary rules in fyredocs_backend/CLAUDE.md §1–§3.
package email

import (
	"context"
	"time"
)

// Mailer sends transactional emails relevant to authentication.
// New email types should be added as additional methods on this interface
// so callers can swap implementations (Resend, SMTP, Noop) without changes.
type Mailer interface {
	SendPasswordReset(ctx context.Context, to, resetURL, requestIP string, ttl time.Duration) error
}

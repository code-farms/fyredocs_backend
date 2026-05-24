package email

import (
	"context"
	"testing"
	"time"
)

func TestNoopMailerSendPasswordReset(t *testing.T) {
	var m Mailer = NoopMailer{}
	err := m.SendPasswordReset(
		context.Background(),
		"user@example.com",
		"https://app.example.com/reset-password?token=abc",
		"127.0.0.1",
		1*time.Hour,
	)
	if err != nil {
		t.Errorf("NoopMailer should not return an error, got %v", err)
	}
}

package handlers

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/utils/tests"

	"auth-service/internal/models"
)

func TestForgotPasswordMalformedJSONReturns200(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ae := &AuthEndpoints{}
	rec := httptest.NewRecorder()
	c, r := gin.CreateTestContext(rec)
	r.POST("/auth/forgot-password", ae.ForgotPassword)
	c.Request = httptest.NewRequest(http.MethodPost, "/auth/forgot-password", strings.NewReader("not-json"))
	c.Request.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, c.Request)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 (no-enumeration), got %d", rec.Code)
	}
}

func TestForgotPasswordEmptyEmailReturns200(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ae := &AuthEndpoints{}
	rec := httptest.NewRecorder()
	c, r := gin.CreateTestContext(rec)
	r.POST("/auth/forgot-password", ae.ForgotPassword)
	c.Request = httptest.NewRequest(http.MethodPost, "/auth/forgot-password",
		bytes.NewBufferString(`{"email":""}`))
	c.Request.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, c.Request)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "If an account exists") {
		t.Errorf("expected generic no-enumeration message, got %s", body)
	}
}

func TestResetPasswordMalformedJSONReturns400(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ae := &AuthEndpoints{}
	rec := httptest.NewRecorder()
	c, r := gin.CreateTestContext(rec)
	r.POST("/auth/reset-password", ae.ResetPassword)
	c.Request = httptest.NewRequest(http.MethodPost, "/auth/reset-password", strings.NewReader("not-json"))
	c.Request.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, c.Request)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "INVALID_INPUT") {
		t.Errorf("expected INVALID_INPUT code, got %s", rec.Body.String())
	}
}

func TestResetPasswordValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name     string
		body     string
		wantCode int
		wantErr  string
	}{
		{
			name:     "empty token",
			body:     `{"token":"","newPassword":"valid-password"}`,
			wantCode: http.StatusBadRequest,
			wantErr:  "INVALID_INPUT",
		},
		{
			name:     "whitespace-only token",
			body:     `{"token":"   ","newPassword":"valid-password"}`,
			wantCode: http.StatusBadRequest,
			wantErr:  "INVALID_INPUT",
		},
		{
			// min=8 binding tag; the old bespoke WEAK_PASSWORD code was folded
			// into the canonical INVALID_INPUT with per-field details.
			name:     "short password",
			body:     `{"token":"some-token","newPassword":"short"}`,
			wantCode: http.StatusBadRequest,
			wantErr:  "INVALID_INPUT",
		},
		{
			name:     "password too long",
			body:     `{"token":"some-token","newPassword":"` + strings.Repeat("a", 129) + `"}`,
			wantCode: http.StatusBadRequest,
			wantErr:  "INVALID_INPUT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ae := &AuthEndpoints{}
			rec := httptest.NewRecorder()
			c, r := gin.CreateTestContext(rec)
			r.POST("/auth/reset-password", ae.ResetPassword)
			c.Request = httptest.NewRequest(http.MethodPost, "/auth/reset-password",
				bytes.NewBufferString(tt.body))
			c.Request.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(rec, c.Request)

			if rec.Code != tt.wantCode {
				t.Errorf("expected %d, got %d (body: %s)", tt.wantCode, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.wantErr) {
				t.Errorf("expected error code %q in body, got %s", tt.wantErr, rec.Body.String())
			}
		})
	}
}

func TestGenerateResetTokenUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		tok, err := generateResetToken()
		if err != nil {
			t.Fatalf("generateResetToken failed: %v", err)
		}
		if tok == "" {
			t.Fatal("expected non-empty token")
		}
		if len(tok) < 40 {
			t.Errorf("expected token at least 40 chars (32 bytes base64url), got %d", len(tok))
		}
		if seen[tok] {
			t.Errorf("duplicate token generated: %s", tok)
		}
		seen[tok] = true
	}
}

// ─── Mailer-failure resilience ────────────────────────────────────────────────

// failingMailer implements email.Mailer and always errors. sent is closed on
// the first invocation so the test can prove the mailer actually ran (a masked
// 200 would otherwise be vacuous — the handler might simply have skipped it).
type failingMailer struct {
	once sync.Once
	sent chan struct{}
}

func newFailingMailer() *failingMailer { return &failingMailer{sent: make(chan struct{})} }

func (m *failingMailer) SendPasswordReset(context.Context, string, string, string, time.Duration) error {
	m.once.Do(func() { close(m.sent) })
	return errors.New("smtp: connection refused")
}

// withDryRunDB swaps models.DB for a gorm handle that builds SQL but never
// executes it (DummyDialector + DryRun): every models.* call succeeds with
// zero rows, which is exactly the shape ForgotPassword sees for a registered
// user, without a database. The global swap follows the capture/swap/restore
// convention (testing-strategy.md) and keeps this test serial — no t.Parallel.
func withDryRunDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(tests.DummyDialector{}, &gorm.Config{
		DryRun: true,
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open dry-run gorm: %v", err)
	}
	prev := models.DB
	models.DB = db
	t.Cleanup(func() { models.DB = prev })
}

// TestForgotPasswordMailerErrorStillMasks pins the endpoint's posture when the
// Mailer errors: ForgotPassword responds 200 with the generic no-enumeration
// message BEFORE the send happens (the mailer runs on a fire-and-forget
// goroutine whose error is only logged), so a mail-provider outage can neither
// 500 the endpoint nor change the response in any way that would let a caller
// distinguish registered from unregistered addresses. NOTE: this masking is
// the designed anti-enumeration behavior (see forgotPasswordSuccessMsg), not a
// bug — the cost is that a user whose reset mail silently fails sees success
// and must retry.
func TestForgotPasswordMailerErrorStillMasks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withDryRunDB(t)

	m := newFailingMailer()
	ae := &AuthEndpoints{Mailer: m}
	rec := httptest.NewRecorder()
	c, r := gin.CreateTestContext(rec)
	r.POST("/auth/forgot-password", ae.ForgotPassword)
	c.Request = httptest.NewRequest(http.MethodPost, "/auth/forgot-password",
		strings.NewReader(`{"email":"someone@example.com"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, c.Request)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — a mailer outage must not change the anti-enumeration response", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "If an account exists") {
		t.Errorf("body = %s, want the generic no-enumeration message", rec.Body.String())
	}

	// The response above was already written; now wait for the background send
	// to prove the erroring mailer WAS invoked and its failure swallowed
	// (logged via slog.Warn), rather than never attempted.
	select {
	case <-m.sent:
	case <-time.After(5 * time.Second):
		t.Fatal("mailer was never invoked — the masked 200 proves nothing about error handling")
	}
}

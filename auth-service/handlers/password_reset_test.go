package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
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
			name:     "short password",
			body:     `{"token":"some-token","newPassword":"short"}`,
			wantCode: http.StatusBadRequest,
			wantErr:  "WEAK_PASSWORD",
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

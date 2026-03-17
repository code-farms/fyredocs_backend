package authverify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWithAuthContextAndFromContext(t *testing.T) {
	authCtx := AuthContext{
		UserID: "user-123",
		Role:   "admin",
		Scope:  []string{"read", "write"},
	}
	ctx := WithAuthContext(context.Background(), authCtx)
	got, ok := FromContext(ctx)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got.UserID != "user-123" {
		t.Errorf("expected UserID 'user-123', got %q", got.UserID)
	}
	if got.Role != "admin" {
		t.Errorf("expected Role 'admin', got %q", got.Role)
	}
}

func TestFromContextNil(t *testing.T) {
	_, ok := FromContext(nil)
	if ok {
		t.Error("expected ok=false for nil context")
	}
}

func TestFromContextMissing(t *testing.T) {
	_, ok := FromContext(context.Background())
	if ok {
		t.Error("expected ok=false for context without auth")
	}
}

func TestSetRequestAuthNil(t *testing.T) {
	got := SetRequestAuth(nil, AuthContext{})
	if got != nil {
		t.Error("expected nil for nil request")
	}
}

func TestSetRequestAuthValid(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	authCtx := AuthContext{UserID: "user-456"}
	got := SetRequestAuth(req, authCtx)
	result, ok := FromContext(got.Context())
	if !ok {
		t.Fatal("expected auth context in request")
	}
	if result.UserID != "user-456" {
		t.Errorf("expected UserID 'user-456', got %q", result.UserID)
	}
}

func TestApplyUserHeaders(t *testing.T) {
	header := http.Header{}
	authCtx := AuthContext{
		UserID:             "user-123",
		Role:               "admin",
		Scope:              []string{"read", "write"},
		Plan:               "pro",
		PlanMaxFileSizeMB:  500,
		PlanMaxFilesPerJob: 50,
	}
	ApplyUserHeaders(header, authCtx)

	if got := header.Get("X-User-ID"); got != "user-123" {
		t.Errorf("expected X-User-ID 'user-123', got %q", got)
	}
	if got := header.Get("X-User-Role"); got != "admin" {
		t.Errorf("expected X-User-Role 'admin', got %q", got)
	}
	if got := header.Get("X-User-Scope"); got != "read write" {
		t.Errorf("expected X-User-Scope 'read write', got %q", got)
	}
	if got := header.Get("X-User-Plan"); got != "pro" {
		t.Errorf("expected X-User-Plan 'pro', got %q", got)
	}
	if got := header.Get("X-User-Plan-Max-File-MB"); got != "500" {
		t.Errorf("expected X-User-Plan-Max-File-MB '500', got %q", got)
	}
	if got := header.Get("X-User-Plan-Max-Files"); got != "50" {
		t.Errorf("expected X-User-Plan-Max-Files '50', got %q", got)
	}
}

func TestApplyUserHeadersNil(t *testing.T) {
	// Should not panic
	ApplyUserHeaders(nil, AuthContext{UserID: "test"})
}

func TestApplyUserHeadersEmptyValues(t *testing.T) {
	header := http.Header{}
	ApplyUserHeaders(header, AuthContext{})
	if got := header.Get("X-User-ID"); got != "" {
		t.Errorf("expected empty X-User-ID, got %q", got)
	}
}

func TestClearUserHeaders(t *testing.T) {
	header := http.Header{}
	header.Set("X-User-ID", "user-123")
	header.Set("X-User-Role", "admin")
	header.Set("X-User-Scope", "read write")
	header.Set("X-User-Plan", "pro")
	header.Set("X-User-Plan-Max-File-MB", "500")
	header.Set("X-User-Plan-Max-Files", "50")
	header.Set("X-Other", "keep-me")

	ClearUserHeaders(header)

	for _, h := range []string{"X-User-ID", "X-User-Role", "X-User-Scope", "X-User-Plan", "X-User-Plan-Max-File-MB", "X-User-Plan-Max-Files"} {
		if got := header.Get(h); got != "" {
			t.Errorf("expected empty %s, got %q", h, got)
		}
	}
	if got := header.Get("X-Other"); got != "keep-me" {
		t.Errorf("expected X-Other 'keep-me', got %q", got)
	}
}

func TestClearUserHeadersNil(t *testing.T) {
	// Should not panic
	ClearUserHeaders(nil)
}

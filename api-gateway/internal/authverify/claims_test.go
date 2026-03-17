package authverify

import (
	"encoding/json"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func TestScopeListUnmarshalString(t *testing.T) {
	data := []byte(`"read write admin"`)
	var scope ScopeList
	if err := json.Unmarshal(data, &scope); err != nil {
		t.Fatal(err)
	}
	if len(scope) != 3 {
		t.Fatalf("expected 3 scopes, got %d: %v", len(scope), scope)
	}
	if scope[0] != "read" || scope[1] != "write" || scope[2] != "admin" {
		t.Errorf("unexpected scopes: %v", scope)
	}
}

func TestScopeListUnmarshalArray(t *testing.T) {
	data := []byte(`["read", "write"]`)
	var scope ScopeList
	if err := json.Unmarshal(data, &scope); err != nil {
		t.Fatal(err)
	}
	if len(scope) != 2 {
		t.Fatalf("expected 2 scopes, got %d", len(scope))
	}
}

func TestScopeListUnmarshalNull(t *testing.T) {
	data := []byte(`null`)
	var scope ScopeList
	if err := json.Unmarshal(data, &scope); err != nil {
		t.Fatal(err)
	}
	if scope != nil {
		t.Errorf("expected nil scope, got %v", scope)
	}
}

func TestScopeListUnmarshalEmpty(t *testing.T) {
	data := []byte(`""`)
	var scope ScopeList
	if err := json.Unmarshal(data, &scope); err != nil {
		t.Fatal(err)
	}
	if len(scope) != 0 {
		t.Errorf("expected 0 scopes for empty string, got %d", len(scope))
	}
}

func TestSplitScope(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"read write", 2},
		{"read,write,admin", 3},
		{"  read  ,  write  ", 2},
		{"", 0},
		{"single", 1},
	}
	for _, tt := range tests {
		got := splitScope(tt.input)
		if len(got) != tt.want {
			t.Errorf("splitScope(%q) len = %d, want %d", tt.input, len(got), tt.want)
		}
	}
}

func TestClaimsToAuthContext(t *testing.T) {
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "user-123",
		},
		Role:               "admin",
		Scope:              ScopeList{"read", "write"},
		Plan:               "pro",
		PlanMaxFileSizeMB:  500,
		PlanMaxFilesPerJob: 50,
	}

	ctx := claims.ToAuthContext()
	if ctx.UserID != "user-123" {
		t.Errorf("expected UserID 'user-123', got %q", ctx.UserID)
	}
	if ctx.Role != "admin" {
		t.Errorf("expected Role 'admin', got %q", ctx.Role)
	}
	if len(ctx.Scope) != 2 {
		t.Errorf("expected 2 scopes, got %d", len(ctx.Scope))
	}
	if ctx.Plan != "pro" {
		t.Errorf("expected Plan 'pro', got %q", ctx.Plan)
	}
	if ctx.PlanMaxFileSizeMB != 500 {
		t.Errorf("expected PlanMaxFileSizeMB 500, got %d", ctx.PlanMaxFileSizeMB)
	}
	if ctx.PlanMaxFilesPerJob != 50 {
		t.Errorf("expected PlanMaxFilesPerJob 50, got %d", ctx.PlanMaxFilesPerJob)
	}
}

func TestClaimsToAuthContextEmptySubject(t *testing.T) {
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "  ",
		},
		Role: "user",
	}
	ctx := claims.ToAuthContext()
	if ctx.UserID != "" {
		t.Errorf("expected empty UserID for whitespace subject, got %q", ctx.UserID)
	}
}

func TestClaimsToAuthContextFilterEmptyScopes(t *testing.T) {
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "user-123",
		},
		Scope: ScopeList{"read", "", "  ", "write"},
	}
	ctx := claims.ToAuthContext()
	if len(ctx.Scope) != 2 {
		t.Errorf("expected 2 non-empty scopes, got %d: %v", len(ctx.Scope), ctx.Scope)
	}
}

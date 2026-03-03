package auth

import (
	"testing"
)

func TestIsAuthenticated(t *testing.T) {
	tests := []struct {
		name string
		ctx  AuthContext
		want bool
	}{
		{
			name: "authenticated user",
			ctx:  AuthContext{UserID: "user-1", Role: "user"},
			want: true,
		},
		{
			name: "admin user",
			ctx:  AuthContext{UserID: "admin-1", Role: "admin"},
			want: true,
		},
		{
			name: "guest is not authenticated",
			ctx:  AuthContext{UserID: "guest-1", Role: "guest", IsGuest: true},
			want: false,
		},
		{
			name: "empty user ID is not authenticated",
			ctx:  AuthContext{UserID: "", Role: "user"},
			want: false,
		},
		{
			name: "whitespace user ID is not authenticated",
			ctx:  AuthContext{UserID: "   ", Role: "user"},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsAuthenticated(tc.ctx)
			if got != tc.want {
				t.Errorf("IsAuthenticated(%+v) = %v, want %v", tc.ctx, got, tc.want)
			}
		})
	}
}

func TestIsGuest(t *testing.T) {
	tests := []struct {
		name string
		ctx  AuthContext
		want bool
	}{
		{
			name: "guest user",
			ctx:  AuthContext{Role: "guest", IsGuest: true},
			want: true,
		},
		{
			name: "authenticated user is not guest",
			ctx:  AuthContext{UserID: "user-1", Role: "user", IsGuest: false},
			want: false,
		},
		{
			name: "empty context is not guest",
			ctx:  AuthContext{},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsGuest(tc.ctx)
			if got != tc.want {
				t.Errorf("IsGuest(%+v) = %v, want %v", tc.ctx, got, tc.want)
			}
		})
	}
}

func TestHasRoleAdmin(t *testing.T) {
	// Admin role should bypass any role check, even for roles the admin
	// does not explicitly hold.
	ctx := AuthContext{UserID: "admin-1", Role: "admin"}

	if !HasRole(ctx, "editor") {
		t.Error("expected admin to bypass HasRole check for 'editor'")
	}
	if !HasRole(ctx, "viewer") {
		t.Error("expected admin to bypass HasRole check for 'viewer'")
	}
	if !HasRole(ctx, "nonexistent-role") {
		t.Error("expected admin to bypass HasRole check for any role")
	}
}

func TestHasRoleMatch(t *testing.T) {
	ctx := AuthContext{UserID: "user-1", Role: "editor"}

	if !HasRole(ctx, "editor") {
		t.Error("expected HasRole to return true when user has matching role")
	}
	if !HasRole(ctx, "viewer", "editor", "moderator") {
		t.Error("expected HasRole to return true when one of the roles matches")
	}
}

func TestHasRoleNoMatch(t *testing.T) {
	ctx := AuthContext{UserID: "user-1", Role: "viewer"}

	if HasRole(ctx, "editor") {
		t.Error("expected HasRole to return false when user lacks the role")
	}
	if HasRole(ctx, "admin-panel", "moderator") {
		t.Error("expected HasRole to return false when none of the roles match")
	}
}

func TestHasRoleEmptyRole(t *testing.T) {
	ctx := AuthContext{UserID: "user-1", Role: ""}

	if HasRole(ctx, "editor") {
		t.Error("expected HasRole to return false when user role is empty")
	}
}

func TestHasScope(t *testing.T) {
	tests := []struct {
		name   string
		ctx    AuthContext
		scopes []string
		want   bool
	}{
		{
			name:   "matching scope",
			ctx:    AuthContext{UserID: "user-1", Role: "user", Scope: []string{"pdf:read", "pdf:write"}},
			scopes: []string{"pdf:read"},
			want:   true,
		},
		{
			name:   "one of multiple scopes matches",
			ctx:    AuthContext{UserID: "user-1", Role: "user", Scope: []string{"pdf:read"}},
			scopes: []string{"pdf:write", "pdf:read"},
			want:   true,
		},
		{
			name:   "no matching scope",
			ctx:    AuthContext{UserID: "user-1", Role: "user", Scope: []string{"pdf:read"}},
			scopes: []string{"pdf:delete"},
			want:   false,
		},
		{
			name:   "empty user scopes",
			ctx:    AuthContext{UserID: "user-1", Role: "user", Scope: []string{}},
			scopes: []string{"pdf:read"},
			want:   false,
		},
		{
			name:   "nil user scopes",
			ctx:    AuthContext{UserID: "user-1", Role: "user", Scope: nil},
			scopes: []string{"pdf:read"},
			want:   false,
		},
		{
			name:   "admin bypasses scope check",
			ctx:    AuthContext{UserID: "admin-1", Role: "admin", Scope: nil},
			scopes: []string{"pdf:delete"},
			want:   true,
		},
		{
			name:   "admin with different casing bypasses scope check",
			ctx:    AuthContext{UserID: "admin-1", Role: "Admin"},
			scopes: []string{"anything"},
			want:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := HasScope(tc.ctx, tc.scopes...)
			if got != tc.want {
				t.Errorf("HasScope(%+v, %v) = %v, want %v", tc.ctx, tc.scopes, got, tc.want)
			}
		})
	}
}

func TestHasScopeAdminBypass(t *testing.T) {
	// Dedicated test to clearly demonstrate admin bypass for scopes.
	ctx := AuthContext{UserID: "admin-1", Role: "admin", Scope: []string{}}

	if !HasScope(ctx, "pdf:read") {
		t.Error("expected admin to bypass scope check even with empty scopes")
	}
	if !HasScope(ctx, "any:scope:at:all") {
		t.Error("expected admin to bypass scope check for arbitrary scope")
	}
}

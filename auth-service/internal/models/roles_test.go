package models

import "testing"

func TestRoleRank(t *testing.T) {
	cases := []struct {
		role string
		want int
	}{
		{RoleUser, 1},
		{RoleAdmin, 2},
		{RoleSuperAdmin, 3},
		{"", 0},
		{"owner", 0},
		{"  admin  ", 2},
	}
	for _, tc := range cases {
		if got := RoleRank(tc.role); got != tc.want {
			t.Errorf("RoleRank(%q) = %d, want %d", tc.role, got, tc.want)
		}
	}
}

func TestCanImpersonate(t *testing.T) {
	cases := []struct {
		caller string
		target string
		want   bool
	}{
		// Full role matrix.
		{RoleUser, RoleUser, false},
		{RoleUser, RoleAdmin, false},
		{RoleUser, RoleSuperAdmin, false},
		{RoleAdmin, RoleUser, true},
		{RoleAdmin, RoleAdmin, false},
		{RoleAdmin, RoleSuperAdmin, false},
		{RoleSuperAdmin, RoleUser, true},
		{RoleSuperAdmin, RoleAdmin, true},
		{RoleSuperAdmin, RoleSuperAdmin, false},
		// Empty target role defaults to "user".
		{RoleAdmin, "", true},
		{RoleUser, "", false},
		// Unknown/empty caller can never impersonate.
		{"", RoleUser, false},
		{"owner", RoleUser, false},
		// Unknown target roles are refused.
		{RoleSuperAdmin, "owner", false},
		// Whitespace is tolerated.
		{" super-admin ", " admin ", true},
	}
	for _, tc := range cases {
		if got := CanImpersonate(tc.caller, tc.target); got != tc.want {
			t.Errorf("CanImpersonate(%q, %q) = %v, want %v", tc.caller, tc.target, got, tc.want)
		}
	}
}

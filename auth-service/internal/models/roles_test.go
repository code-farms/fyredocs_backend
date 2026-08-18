// Tests for the platform role ordering. The invariant under guard:
// impersonation requires strictly greater privilege, unknown or differently
// cased role strings rank below every real role, and nothing — not even a
// super-admin — can impersonate a super-admin or an unrecognized role.
package models

import "testing"

func TestRoleRank(t *testing.T) {
	t.Parallel()
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
		// Guest sessions never appear in users.role (chk_user_role), so the
		// string must rank as unknown, below every real role.
		{"guest", 0},
		// Case matters: a differently cased role would bypass the CHECK
		// constraint values, so it must not inherit the canonical rank.
		{"Admin", 0},
		{"ADMIN", 0},
		{"Super-Admin", 0},
	}
	for _, tc := range cases {
		if got := RoleRank(tc.role); got != tc.want {
			t.Errorf("RoleRank(%q) = %d, want %d", tc.role, got, tc.want)
		}
	}
}

func TestCanImpersonate(t *testing.T) {
	t.Parallel()
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
		// Case-mismatched roles are unknown on both sides: a caller cannot gain
		// the gate with "Admin", and a target under an aliased casing cannot be
		// impersonated even by the top rank.
		{"Admin", RoleUser, false},
		{"ADMIN", RoleUser, false},
		{RoleSuperAdmin, "Admin", false},
		// Guest ranks as unknown: it can never impersonate, and impersonating a
		// guest-role row is refused because its privilege cannot be reasoned about.
		{"guest", RoleUser, false},
		{RoleAdmin, "guest", false},
		{RoleSuperAdmin, "guest", false},
		// Both sides empty: empty caller loses before the target defaulting to
		// "user" can apply.
		{"", "", false},
	}
	for _, tc := range cases {
		if got := CanImpersonate(tc.caller, tc.target); got != tc.want {
			t.Errorf("CanImpersonate(%q, %q) = %v, want %v", tc.caller, tc.target, got, tc.want)
		}
	}
}

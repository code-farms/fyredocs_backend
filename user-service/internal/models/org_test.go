package models

import "testing"

// TestRoleAtLeastFullMatrix pins the complete privilege ordering
// viewer < editor < admin < owner as a 4x4 matrix. Handlers express every
// authorization decision as RoleAtLeast(callerRole, minimum), so a single
// wrong cell here is a privilege-escalation (or lockout) bug in every consumer.
func TestRoleAtLeastFullMatrix(t *testing.T) {
	// Ascending privilege; the index is the expected rank.
	roles := []string{RoleViewer, RoleEditor, RoleAdmin, RoleOwner}
	for i, role := range roles {
		for j, min := range roles {
			want := i >= j
			if got := RoleAtLeast(role, min); got != want {
				t.Errorf("RoleAtLeast(%q, %q) = %v, want %v", role, min, got, want)
			}
		}
	}
}

// TestRoleAtLeastUnknownAndEmptyRoles pins the fail-closed posture for the
// caller side (an unranked role satisfies nothing) and pins the asymmetry on
// the minimum side: an unknown minimum ranks 0, so any known role satisfies it.
//
// NOTE: the unknown-minimum rows document actual behavior, not a desired
// contract — RoleAtLeast(known, "bogus") returns true. Today every call site
// passes a Role* constant as the minimum (user-service/handlers/orgs.go), so
// this is unreachable from request data; if a minimum ever comes from input,
// this pin is the alarm (docs/developer/architecture/backend-hardening.md).
func TestRoleAtLeastUnknownAndEmptyRoles(t *testing.T) {
	cases := []struct {
		name      string
		role, min string
		want      bool
	}{
		{"empty role satisfies nothing", "", RoleViewer, false},
		{"unknown role satisfies nothing", "bogus", RoleViewer, false},
		{"unknown role vs unknown min", "bogus", "bogus", false},
		{"empty role vs empty min", "", "", false},
		{"known role vs unknown min (pinned true)", RoleViewer, "bogus", true},
		{"known role vs empty min (pinned true)", RoleOwner, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RoleAtLeast(tc.role, tc.min); got != tc.want {
				t.Errorf("RoleAtLeast(%q, %q) = %v, want %v", tc.role, tc.min, got, tc.want)
			}
		})
	}
}

// TestRoleAtLeastIsCaseSensitive pins that role matching is exact:
// a case-variant of a real role ranks 0 and is denied everything. Roles are
// stored lowercase by this service's own handlers, so a case-insensitive match
// would only ever widen access for data that bypassed those handlers.
func TestRoleAtLeastIsCaseSensitive(t *testing.T) {
	for _, role := range []string{"Owner", "OWNER", "Admin", "ADMIN", "Editor", "Viewer"} {
		if RoleAtLeast(role, RoleViewer) {
			t.Errorf("RoleAtLeast(%q, viewer) = true, want false (case-variant must not rank)", role)
		}
		if ValidRole(role) {
			t.Errorf("ValidRole(%q) = true, want false (case-variant must not validate)", role)
		}
	}
}

// TestValidRole pins the closed set of assignable roles. "owner" IS a valid
// role here — the member-mutation handlers layer their own newRole != owner
// check on top, so this function alone must never be used to gate a grant.
func TestValidRole(t *testing.T) {
	valid := []string{RoleOwner, RoleAdmin, RoleEditor, RoleViewer}
	for _, r := range valid {
		if !ValidRole(r) {
			t.Errorf("ValidRole(%q) = false, want true", r)
		}
	}
	invalid := []string{"", "superadmin", "root", "member", " owner", "owner ", "owner\n"}
	for _, r := range invalid {
		if ValidRole(r) {
			t.Errorf("ValidRole(%q) = true, want false", r)
		}
	}
}

package models

import "strings"

// Platform-wide user roles, ordered by privilege. These mirror the values
// enforced by the chk_user_role CHECK constraint on users.role.
const (
	RoleUser       = "user"
	RoleAdmin      = "admin"
	RoleSuperAdmin = "super-admin"
)

// platformRoleRank maps each platform role to its privilege rank. Higher rank
// means more privilege. Unknown roles rank 0 (below every real role).
var platformRoleRank = map[string]int{
	RoleUser:       1,
	RoleAdmin:      2,
	RoleSuperAdmin: 3,
}

// RoleRank returns the privilege rank for a platform role, or 0 for unknown
// or empty roles.
func RoleRank(role string) int {
	return platformRoleRank[strings.TrimSpace(role)]
}

// CanImpersonate reports whether a caller with callerRole may impersonate a
// user with targetRole. Impersonation requires strictly greater privilege:
// admins may impersonate users, super-admins may impersonate users and admins,
// and nobody — not even another super-admin — may impersonate a super-admin.
// An empty target role is treated as "user" (the default assigned at login);
// an unknown or empty caller role can never impersonate.
func CanImpersonate(callerRole, targetRole string) bool {
	caller := RoleRank(callerRole)
	if caller == 0 {
		return false
	}
	target := strings.TrimSpace(targetRole)
	if target == "" {
		target = RoleUser
	}
	rank, known := platformRoleRank[target]
	if !known {
		// Unknown target roles are refused outright: we cannot reason about
		// their privilege, so impersonating them is never safe.
		return false
	}
	return caller > rank
}

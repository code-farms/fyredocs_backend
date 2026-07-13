package authverify

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"fyredocs/shared/response"
)

// IsAuthenticated reports whether the context belongs to a real, non-guest user.
func IsAuthenticated(authCtx AuthContext) bool {
	return strings.TrimSpace(authCtx.UserID) != "" && !authCtx.IsGuest
}

// IsGuest reports whether the context belongs to an anonymous guest.
func IsGuest(authCtx AuthContext) bool {
	return authCtx.IsGuest
}

// HasRole reports whether the caller holds any of the given roles. Admins are
// always granted. Matches use a constant-time comparison to avoid leaking role
// names via timing.
func HasRole(authCtx AuthContext, roles ...string) bool {
	if strings.EqualFold(authCtx.Role, "admin") {
		return true
	}
	for _, role := range roles {
		if secureEqual(authCtx.Role, role) {
			return true
		}
	}
	return false
}

// HasScope reports whether the caller holds any of the given scopes. Admins are
// always granted; scope matching is constant-time.
func HasScope(authCtx AuthContext, scopes ...string) bool {
	if strings.EqualFold(authCtx.Role, "admin") {
		return true
	}
	if len(authCtx.Scope) == 0 {
		return false
	}
	for _, required := range scopes {
		for _, granted := range authCtx.Scope {
			if secureEqual(granted, required) {
				return true
			}
		}
	}
	return false
}

// RequireAuthenticatedGin returns middleware that rejects unauthenticated or
// guest requests with 401/403 before the handler runs.
func RequireAuthenticatedGin() gin.HandlerFunc {
	return func(c *gin.Context) {
		authCtx, ok := GetGinAuth(c)
		if !ok || strings.TrimSpace(authCtx.UserID) == "" {
			response.AbortErr(c, http.StatusUnauthorized, string(ErrCodeUnauthorized), "Your session has expired. Please log in again.")
			return
		}
		if authCtx.IsGuest {
			response.AbortErr(c, http.StatusForbidden, string(ErrCodeForbidden), "You don't have permission to perform this action.")
			return
		}
		c.Next()
	}
}

// RequireRoleGin returns middleware that admits only authenticated non-guest
// callers who hold one of the given roles (admins always pass).
func RequireRoleGin(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		authCtx, ok := GetGinAuth(c)
		if !ok || strings.TrimSpace(authCtx.UserID) == "" {
			response.AbortErr(c, http.StatusUnauthorized, string(ErrCodeUnauthorized), "Your session has expired. Please log in again.")
			return
		}
		if authCtx.IsGuest {
			response.AbortErr(c, http.StatusForbidden, string(ErrCodeForbidden), "You don't have permission to perform this action.")
			return
		}
		if !HasRole(authCtx, roles...) {
			response.AbortErr(c, http.StatusForbidden, string(ErrCodeForbidden), "You don't have permission to perform this action.")
			return
		}
		c.Next()
	}
}

// RequireScopeGin returns middleware that admits only authenticated non-guest
// callers who hold one of the given scopes (admins always pass).
func RequireScopeGin(scopes ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		authCtx, ok := GetGinAuth(c)
		if !ok || strings.TrimSpace(authCtx.UserID) == "" {
			response.AbortErr(c, http.StatusUnauthorized, string(ErrCodeUnauthorized), "Your session has expired. Please log in again.")
			return
		}
		if authCtx.IsGuest {
			response.AbortErr(c, http.StatusForbidden, string(ErrCodeForbidden), "You don't have permission to perform this action.")
			return
		}
		if !HasScope(authCtx, scopes...) {
			response.AbortErr(c, http.StatusForbidden, string(ErrCodeForbidden), "You don't have permission to perform this action.")
			return
		}
		c.Next()
	}
}

// secureEqual compares two strings in constant time, returning false for empty
// or length-mismatched inputs, so authorization checks don't leak values via timing.
func secureEqual(left string, right string) bool {
	if left == "" || right == "" {
		return false
	}
	leftBytes := []byte(left)
	rightBytes := []byte(right)
	if len(leftBytes) != len(rightBytes) {
		return false
	}
	return subtle.ConstantTimeCompare(leftBytes, rightBytes) == 1
}

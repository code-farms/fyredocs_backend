package authverify

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"fyredocs/shared/response"
)

func IsAuthenticated(authCtx AuthContext) bool {
	return strings.TrimSpace(authCtx.UserID) != "" && !authCtx.IsGuest
}

func IsGuest(authCtx AuthContext) bool {
	return authCtx.IsGuest
}

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

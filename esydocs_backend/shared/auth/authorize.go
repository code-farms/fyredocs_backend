package auth

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"esydocs/shared/response"
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
			response.AbortErr(c, http.StatusUnauthorized, string(ErrCodeUnauthorized), "Invalid or expired token")
			return
		}
		if authCtx.IsGuest {
			response.AbortErr(c, http.StatusForbidden, string(ErrCodeForbidden), "Insufficient permissions")
			return
		}
		c.Next()
	}
}

func RequireRoleGin(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		authCtx, ok := GetGinAuth(c)
		if !ok || strings.TrimSpace(authCtx.UserID) == "" {
			response.AbortErr(c, http.StatusUnauthorized, string(ErrCodeUnauthorized), "Invalid or expired token")
			return
		}
		if authCtx.IsGuest {
			response.AbortErr(c, http.StatusForbidden, string(ErrCodeForbidden), "Insufficient permissions")
			return
		}
		if !HasRole(authCtx, roles...) {
			response.AbortErr(c, http.StatusForbidden, string(ErrCodeForbidden), "Insufficient permissions")
			return
		}
		c.Next()
	}
}

func RequireScopeGin(scopes ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		authCtx, ok := GetGinAuth(c)
		if !ok || strings.TrimSpace(authCtx.UserID) == "" {
			response.AbortErr(c, http.StatusUnauthorized, string(ErrCodeUnauthorized), "Invalid or expired token")
			return
		}
		if authCtx.IsGuest {
			response.AbortErr(c, http.StatusForbidden, string(ErrCodeForbidden), "Insufficient permissions")
			return
		}
		if !HasScope(authCtx, scopes...) {
			response.AbortErr(c, http.StatusForbidden, string(ErrCodeForbidden), "Insufficient permissions")
			return
		}
		c.Next()
	}
}

func RequireAuthenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authCtx, ok := FromContext(r.Context())
		if !ok || strings.TrimSpace(authCtx.UserID) == "" {
			WriteError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "Invalid or expired token")
			return
		}
		if authCtx.IsGuest {
			WriteError(w, http.StatusForbidden, ErrCodeForbidden, "Insufficient permissions")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func RequireRole(next http.Handler, roles ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authCtx, ok := FromContext(r.Context())
		if !ok || strings.TrimSpace(authCtx.UserID) == "" {
			WriteError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "Invalid or expired token")
			return
		}
		if authCtx.IsGuest {
			WriteError(w, http.StatusForbidden, ErrCodeForbidden, "Insufficient permissions")
			return
		}
		if !HasRole(authCtx, roles...) {
			WriteError(w, http.StatusForbidden, ErrCodeForbidden, "Insufficient permissions")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func RequireScope(next http.Handler, scopes ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authCtx, ok := FromContext(r.Context())
		if !ok || strings.TrimSpace(authCtx.UserID) == "" {
			WriteError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "Invalid or expired token")
			return
		}
		if authCtx.IsGuest {
			WriteError(w, http.StatusForbidden, ErrCodeForbidden, "Insufficient permissions")
			return
		}
		if !HasScope(authCtx, scopes...) {
			WriteError(w, http.StatusForbidden, ErrCodeForbidden, "Insufficient permissions")
			return
		}
		next.ServeHTTP(w, r)
	})
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

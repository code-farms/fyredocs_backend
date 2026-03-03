package authverify

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"
)

type AuthContext struct {
	UserID  string
	Role    string
	Scope   []string
	IsGuest bool
}

type authContextKey struct{}

var authKey = authContextKey{}

func WithAuthContext(ctx context.Context, authCtx AuthContext) context.Context {
	return context.WithValue(ctx, authKey, authCtx)
}

func FromContext(ctx context.Context) (AuthContext, bool) {
	if ctx == nil {
		return AuthContext{}, false
	}
	value := ctx.Value(authKey)
	authCtx, ok := value.(AuthContext)
	return authCtx, ok
}

func SetGinAuth(c *gin.Context, authCtx AuthContext) {
	if c == nil {
		return
	}
	c.Set("auth", authCtx)
	c.Request = c.Request.WithContext(WithAuthContext(c.Request.Context(), authCtx))
}

func GetGinAuth(c *gin.Context) (AuthContext, bool) {
	if c == nil {
		return AuthContext{}, false
	}
	if value, ok := c.Get("auth"); ok {
		if authCtx, ok := value.(AuthContext); ok {
			return authCtx, true
		}
	}
	return FromContext(c.Request.Context())
}

func IsAuthenticated(authCtx AuthContext) bool {
	return strings.TrimSpace(authCtx.UserID) != "" && !authCtx.IsGuest
}

package authverify

import (
	"context"
	"net/http"
	"strings"
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

func SetRequestAuth(r *http.Request, authCtx AuthContext) *http.Request {
	if r == nil {
		return r
	}
	return r.WithContext(WithAuthContext(r.Context(), authCtx))
}

func ApplyUserHeaders(header http.Header, authCtx AuthContext) {
	if header == nil {
		return
	}
	if strings.TrimSpace(authCtx.UserID) != "" {
		header.Set("X-User-ID", authCtx.UserID)
	}
	if strings.TrimSpace(authCtx.Role) != "" {
		header.Set("X-User-Role", authCtx.Role)
	}
	if len(authCtx.Scope) > 0 {
		header.Set("X-User-Scope", strings.Join(authCtx.Scope, " "))
	}
}

func ClearUserHeaders(header http.Header) {
	if header == nil {
		return
	}
	header.Del("X-User-ID")
	header.Del("X-User-Role")
	header.Del("X-User-Scope")
}

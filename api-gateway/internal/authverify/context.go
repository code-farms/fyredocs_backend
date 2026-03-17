package authverify

import (
	"context"
	"net/http"
	"strconv"
	"strings"
)

type AuthContext struct {
	UserID             string
	Role               string
	Scope              []string
	Plan               string
	PlanMaxFileSizeMB  int
	PlanMaxFilesPerJob int
	IsGuest            bool
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
	if strings.TrimSpace(authCtx.Plan) != "" {
		header.Set("X-User-Plan", authCtx.Plan)
	}
	if authCtx.PlanMaxFileSizeMB > 0 {
		header.Set("X-User-Plan-Max-File-MB", strconv.Itoa(authCtx.PlanMaxFileSizeMB))
	}
	if authCtx.PlanMaxFilesPerJob > 0 {
		header.Set("X-User-Plan-Max-Files", strconv.Itoa(authCtx.PlanMaxFilesPerJob))
	}
}

func ClearUserHeaders(header http.Header) {
	if header == nil {
		return
	}
	header.Del("X-User-ID")
	header.Del("X-User-Role")
	header.Del("X-User-Scope")
	header.Del("X-User-Plan")
	header.Del("X-User-Plan-Max-File-MB")
	header.Del("X-User-Plan-Max-Files")
}

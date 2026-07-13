package authverify

import (
	"context"
	"net/http"
	"strconv"
	"strings"
)

// AuthContext is the verified identity and plan/authorization data carried
// through a request after authentication. The gateway resolves it once and
// forwards it to upstream services via the X-User-* headers below.
type AuthContext struct {
	UserID             string
	Role               string
	Scope              []string
	Plan               string
	PlanMaxFileSizeMB  int
	PlanMaxFilesPerJob int
	IsGuest            bool
	// ImpersonatedBy is the admin user ID when this request is an impersonation
	// (proxy login) session; empty otherwise.
	ImpersonatedBy string
}

type authContextKey struct{}

var authKey = authContextKey{}

// WithAuthContext returns a copy of ctx carrying the given AuthContext.
func WithAuthContext(ctx context.Context, authCtx AuthContext) context.Context {
	return context.WithValue(ctx, authKey, authCtx)
}

// FromContext extracts the AuthContext previously stored by WithAuthContext;
// ok is false when none is present.
func FromContext(ctx context.Context) (AuthContext, bool) {
	if ctx == nil {
		return AuthContext{}, false
	}
	value := ctx.Value(authKey)
	authCtx, ok := value.(AuthContext)
	return authCtx, ok
}

// SetRequestAuth returns r with the AuthContext attached to its context.
func SetRequestAuth(r *http.Request, authCtx AuthContext) *http.Request {
	if r == nil {
		return r
	}
	return r.WithContext(WithAuthContext(r.Context(), authCtx))
}

// ApplyUserHeaders writes the AuthContext onto the outbound X-User-* headers so
// upstream services receive the caller's identity. Only non-empty fields are set.
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
	if strings.TrimSpace(authCtx.ImpersonatedBy) != "" {
		header.Set("X-Impersonated-By", authCtx.ImpersonatedBy)
	}
}

// ClearUserHeaders strips all X-User-* headers. The gateway calls this on every
// inbound request before applying its own, so a client cannot spoof identity by
// sending these headers directly.
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
	header.Del("X-Impersonated-By")
}

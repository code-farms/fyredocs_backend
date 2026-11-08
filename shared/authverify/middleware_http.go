package authverify

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"fyredocs/shared/config"
)

// guestPlanContext returns the starting limits for a request with no valid
// token. They are fallbacks only: the gateway's PlanResolver then replaces them
// with the guest row of auth-service's subscription_plans table (Redis key
// plan:guest, see api-gateway/internal/plancache), which is the source of truth.
// These config defaults apply only when that lookup has nothing.
func guestPlanContext() AuthContext {
	return AuthContext{
		Plan:               "guest",
		PlanMaxFileSizeMB:  config.GuestMaxFileSizeMB(),
		PlanMaxFilesPerJob: config.GuestMaxFilesPerJob(),
		PlanMaxJobInputMB:  config.GuestMaxJobInputMB(),
	}
}

// PlanResolver resolves plan info for a user and enriches the AuthContext.
type PlanResolver func(r *http.Request, authCtx *AuthContext)

// HTTPMiddlewareOptions configures HTTPAuthMiddleware. It mirrors
// GinMiddlewareOptions and adds a PlanResolver hook (used by the gateway to
// enrich the context with plan limits) and PublicPaths that always skip
// verification.
type HTTPMiddlewareOptions struct {
	Verifier              *Verifier
	GuestStore            GuestStore
	TrustGatewayHeaders   bool
	GuestTokenHeaderName  string
	GuestCookieName       string
	AccessTokenCookieName string
	ResolvePlan           PlanResolver
	// PublicPaths lists URL paths that skip token verification entirely.
	// Requests to these paths are always treated as guest, even if a
	// stale access_token cookie is present. This prevents an authentication
	// deadlock where expired cookies block login/signup/refresh endpoints.
	PublicPaths []string
	// PublicPrefixes extends PublicPaths for routes with variable segments
	// (e.g. "/share/" for /share/:token, where the token IS the credential).
	// Entries should end with "/" so "/share/x" matches but "/shares" doesn't.
	PublicPrefixes []string
}

// HTTPAuthMiddleware is the net/http counterpart to GinAuthMiddleware, used by
// the gateway. It verifies the caller (gateway headers, bearer token, or guest
// token), optionally resolves plan limits, applies guest defaults on public
// paths, and forwards the resolved identity to upstream services.
func HTTPAuthMiddleware(options HTTPMiddlewareOptions) func(http.Handler) http.Handler {
	guestCookieName := options.GuestCookieName
	if guestCookieName == "" {
		guestCookieName = "guest_token"
	}
	accessCookieName := options.AccessTokenCookieName
	if accessCookieName == "" {
		accessCookieName = "access_token"
	}

	publicSet := make(map[string]struct{}, len(options.PublicPaths))
	for _, p := range options.PublicPaths {
		publicSet[strings.TrimRight(p, "/")] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r == nil {
				WriteError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "Invalid or expired token")
				return
			}
			if r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}

			// Public paths bypass token verification to prevent stale-cookie deadlock.
			if _, ok := publicSet[strings.TrimRight(r.URL.Path, "/")]; ok {
				next.ServeHTTP(w, SetRequestAuth(r, guestPlanContext()))
				return
			}
			for _, prefix := range options.PublicPrefixes {
				if strings.HasPrefix(r.URL.Path, prefix) {
					next.ServeHTTP(w, SetRequestAuth(r, guestPlanContext()))
					return
				}
			}

			if options.TrustGatewayHeaders {
				if authCtx, ok := authContextFromGatewayHeaders(r.Header); ok {
					next.ServeHTTP(w, SetRequestAuth(r, authCtx))
					return
				}
			}

			guestCtx, _ := guestContextFromCookie(r, options.GuestStore, guestCookieName)

			// Try Authorization header first
			token, hasToken := extractBearerToken(r.Header.Get("Authorization"))

			// If no Authorization header, try cookie
			if !hasToken {
				if cookie, err := r.Cookie(accessCookieName); err == nil {
					token = strings.TrimSpace(cookie.Value)
					hasToken = token != ""
				}
			}

			if hasToken {
				if options.Verifier == nil {
					WriteError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "Invalid or expired token")
					return
				}
				claims, err := options.Verifier.Verify(r.Context(), token)
				if err != nil {
					slog.WarnContext(r.Context(), "token verification failed", "error", err, "op", "auth.verify_token", "path", r.URL.Path, "method", r.Method)
					code, msg := ErrCodeUnauthorized, "Your session is invalid. Please log in again."
					if errors.Is(err, ErrTokenExpired) {
						code, msg = ErrCodeTokenExpired, "Your session has expired. Please refresh or log in again."
					}
					WriteError(w, http.StatusUnauthorized, code, msg)
					return
				}
				authCtx := claims.ToAuthContext()
				if options.ResolvePlan != nil {
					options.ResolvePlan(r, &authCtx)
				}
				next.ServeHTTP(w, SetRequestAuth(r, authCtx))
				return
			}

			// Guests (guest cookie or no credentials at all) go through the same
			// PlanResolver as users, so their limits come from the guest plan row
			// rather than the config defaults. Without this the gateway never
			// consulted the guest row and a DB edit to it had no effect.
			if guestCtx != nil {
				authCtx := *guestCtx
				resolveGuestPlan(r, &authCtx, options.ResolvePlan)
				next.ServeHTTP(w, SetRequestAuth(r, authCtx))
				return
			}

			// No token — unauthenticated request. Apply guest plan limits.
			authCtx := guestPlanContext()
			resolveGuestPlan(r, &authCtx, options.ResolvePlan)
			next.ServeHTTP(w, SetRequestAuth(r, authCtx))
		})
	}
}

// resolveGuestPlan fills a guest context's plan limits: config fallbacks
// first, then the resolver (the gateway's lookup of the guest plan row), which
// sees IsGuest=true. The context's identity fields are left as they were; only
// IsGuest is forced for the resolver call and restored afterwards, so a
// credential-less context does not start reporting itself as a guest-token
// holder to authorization checks.
func resolveGuestPlan(r *http.Request, authCtx *AuthContext, resolve PlanResolver) {
	fallback := guestPlanContext()
	authCtx.Plan = fallback.Plan
	authCtx.PlanMaxFileSizeMB = fallback.PlanMaxFileSizeMB
	authCtx.PlanMaxFilesPerJob = fallback.PlanMaxFilesPerJob
	authCtx.PlanMaxJobInputMB = fallback.PlanMaxJobInputMB
	if resolve == nil {
		return
	}
	wasGuest := authCtx.IsGuest
	authCtx.IsGuest = true
	resolve(r, authCtx)
	authCtx.IsGuest = wasGuest
}

// SplitScopes parses a space/comma-delimited scope header into a cleaned slice.
func SplitScopes(header string) []string {
	return splitScope(strings.TrimSpace(header))
}

// guestContextFromCookie resolves a guest identity from the guest cookie
// ONLY. The HTTP (gateway) middleware deliberately ignores the X-Guest-Token
// header at the edge; the Gin middleware used by internal services accepts
// header-then-cookie (see guestContextFromRequest in middleware_gin.go).
func guestContextFromCookie(r *http.Request, store GuestStore, cookieName string) (*AuthContext, error) {
	if r == nil || store == nil {
		return nil, nil
	}
	var token string
	if cookie, err := r.Cookie(cookieName); err == nil {
		token = strings.TrimSpace(cookie.Value)
	}
	if token == "" {
		return nil, nil
	}
	valid, err := store.ValidateGuestToken(r.Context(), token)
	if err != nil || !valid {
		return nil, err
	}
	return &AuthContext{
		Role:    "guest",
		IsGuest: true,
	}, nil
}

// authContextFromGatewayHeaders reconstructs identity from headers the gateway
// injected, skipping token verification.
//
// This is ONLY sound when the caller cannot be anything but the gateway, which is
// why it sits behind an explicit opt-in flag that defaults to off. A service that
// enabled it and was then exposed directly would accept a caller-supplied
// X-User-ID as proof of identity.
func authContextFromGatewayHeaders(header http.Header) (AuthContext, bool) {
	if header == nil {
		return AuthContext{}, false
	}
	userID := strings.TrimSpace(header.Get("X-User-ID"))
	if userID == "" {
		return AuthContext{}, false
	}
	role := strings.TrimSpace(header.Get("X-User-Role"))
	scope := splitScope(header.Get("X-User-Scope"))
	plan := strings.TrimSpace(header.Get("X-User-Plan"))
	maxFileMB, _ := strconv.Atoi(header.Get("X-User-Plan-Max-File-MB"))
	maxFiles, _ := strconv.Atoi(header.Get("X-User-Plan-Max-Files"))
	maxJobMB, _ := strconv.Atoi(header.Get("X-User-Plan-Max-Job-MB"))
	return AuthContext{
		UserID:             userID,
		Role:               role,
		Scope:              scope,
		Plan:               plan,
		PlanMaxFileSizeMB:  maxFileMB,
		PlanMaxFilesPerJob: maxFiles,
		PlanMaxJobInputMB:  maxJobMB,
	}, true
}

// extractBearerToken pulls the token out of an Authorization header, requiring the
// "Bearer" scheme. Locating a token is not verifying one; the caller must still
// verify.
func extractBearerToken(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 {
		return "", false
	}
	if !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	token := strings.TrimSpace(parts[1])
	if token == "" {
		return "", false
	}
	return token, true
}

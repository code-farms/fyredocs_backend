package authverify

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"fyredocs/shared/config"
)

// guestPlanContext returns the limits applied to requests with no valid token.
// The values come from shared/config, which also seeds the "guest" row in the
// subscription_plans table — so guest enforcement matches what /auth/plans
// serves to the frontend (the DB is the source of truth).
func guestPlanContext() AuthContext {
	return AuthContext{
		Plan:               "guest",
		PlanMaxFileSizeMB:  config.GuestMaxFileSizeMB(),
		PlanMaxFilesPerJob: config.GuestMaxFilesPerJob(),
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
					WriteError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "Invalid or expired token")
					return
				}
				authCtx := claims.ToAuthContext()
				if options.ResolvePlan != nil {
					options.ResolvePlan(r, &authCtx)
				}
				next.ServeHTTP(w, SetRequestAuth(r, authCtx))
				return
			}

			if guestCtx != nil {
				next.ServeHTTP(w, SetRequestAuth(r, *guestCtx))
				return
			}

			// No token — unauthenticated request. Apply guest plan limits.
			next.ServeHTTP(w, SetRequestAuth(r, guestPlanContext()))
		})
	}
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
	return AuthContext{
		UserID:             userID,
		Role:               role,
		Scope:              scope,
		Plan:               plan,
		PlanMaxFileSizeMB:  maxFileMB,
		PlanMaxFilesPerJob: maxFiles,
	}, true
}

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

package authverify

import (
	"net/http"
	"strconv"
	"strings"
)

// anonymousPlan holds the limits applied to requests with no valid token.
// These mirror the "anonymous" row in the subscription_plans table.
// Override via ANON_MAX_FILE_MB and ANON_MAX_FILES env vars if needed.
var anonymousPlan = struct {
	Name           string
	MaxFileSizeMB  int
	MaxFilesPerJob int
}{
	Name:           "anonymous",
	MaxFileSizeMB:  10,
	MaxFilesPerJob: 5,
}

// PlanResolver resolves plan info for a user and enriches the AuthContext.
type PlanResolver func(r *http.Request, authCtx *AuthContext)

type HTTPMiddlewareOptions struct {
	Verifier              *Verifier
	GuestStore            GuestStore
	TrustGatewayHeaders   bool
	GuestTokenHeaderName  string
	GuestCookieName       string
	AccessTokenCookieName string
	ResolvePlan           PlanResolver
	// PublicPaths lists URL paths that skip token verification entirely.
	// Requests to these paths are always treated as anonymous, even if a
	// stale access_token cookie is present. This prevents an authentication
	// deadlock where expired cookies block login/signup/refresh endpoints.
	PublicPaths []string
}

func HTTPAuthMiddleware(options HTTPMiddlewareOptions) func(http.Handler) http.Handler {
	headerName := options.GuestTokenHeaderName
	if headerName == "" {
		headerName = "X-Guest-Token"
	}
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
				anonCtx := AuthContext{
					Plan:               anonymousPlan.Name,
					PlanMaxFileSizeMB:  anonymousPlan.MaxFileSizeMB,
					PlanMaxFilesPerJob: anonymousPlan.MaxFilesPerJob,
				}
				next.ServeHTTP(w, SetRequestAuth(r, anonCtx))
				return
			}

			if options.TrustGatewayHeaders {
				if authCtx, ok := authContextFromGatewayHeaders(r.Header); ok {
					next.ServeHTTP(w, SetRequestAuth(r, authCtx))
					return
				}
			}

			guestCtx, _ := guestContextFromRequest(r, options.GuestStore, headerName, guestCookieName)

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

			// No token — anonymous request. Apply anonymous plan limits.
			anonCtx := AuthContext{
				Plan:               anonymousPlan.Name,
				PlanMaxFileSizeMB:  anonymousPlan.MaxFileSizeMB,
				PlanMaxFilesPerJob: anonymousPlan.MaxFilesPerJob,
			}
			next.ServeHTTP(w, SetRequestAuth(r, anonCtx))
		})
	}
}

func SplitScopes(header string) []string {
	return splitScope(strings.TrimSpace(header))
}

func guestContextFromRequest(r *http.Request, store GuestStore, _ string, cookieName string) (*AuthContext, error) {
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

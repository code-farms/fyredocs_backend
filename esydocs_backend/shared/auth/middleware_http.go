package auth

import (
	"net/http"
	"strings"
)

type HTTPMiddlewareOptions struct {
	Verifier              *Verifier
	GuestStore            GuestStore
	TrustGatewayHeaders   bool
	GuestTokenHeaderName  string
	GuestCookieName       string
	AccessTokenCookieName string
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
				next.ServeHTTP(w, SetRequestAuth(r, claims.ToAuthContext()))
				return
			}

			if guestCtx != nil {
				next.ServeHTTP(w, SetRequestAuth(r, *guestCtx))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func SplitScopes(header string) []string {
	return splitScope(strings.TrimSpace(header))
}

package auth

import (
	"net/http"
	"strings"
)

type HTTPMiddlewareOptions struct {
	Verifier             *Verifier
	GuestStore           GuestStore
	TrustGatewayHeaders  bool
	GuestTokenHeaderName string
	GuestCookieName      string
}

func HTTPAuthMiddleware(options HTTPMiddlewareOptions) func(http.Handler) http.Handler {
	headerName := options.GuestTokenHeaderName
	if headerName == "" {
		headerName = "X-Guest-Token"
	}
	cookieName := options.GuestCookieName
	if cookieName == "" {
		cookieName = "guest_token"
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

			guestCtx, _ := guestContextFromRequest(r, options.GuestStore, headerName, cookieName)

			token, hasToken := extractBearerToken(r.Header.Get("Authorization"))
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

package authverify

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"fyredocs/shared/response"
)

type GinMiddlewareOptions struct {
	Verifier              *Verifier
	GuestStore            GuestStore
	TrustGatewayHeaders   bool
	GuestTokenHeaderName  string
	GuestCookieName       string
	AccessTokenCookieName string
}

func GinAuthMiddleware(options GinMiddlewareOptions) gin.HandlerFunc {
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

	return func(c *gin.Context) {
		if c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}

		if options.TrustGatewayHeaders {
			if authCtx, ok := authContextFromGatewayHeaders(c.Request.Header); ok {
				SetGinAuth(c, authCtx)
				c.Next()
				return
			}
		}

		guestCtx, _ := guestContextFromRequest(c.Request, options.GuestStore, headerName, guestCookieName)

		token, hasToken := extractBearerToken(c.GetHeader("Authorization"))

		if !hasToken {
			if cookie, err := c.Cookie(accessCookieName); err == nil {
				token = strings.TrimSpace(cookie)
				hasToken = token != ""
			}
		}

		if hasToken {
			if options.Verifier == nil {
				response.AbortErr(c, http.StatusUnauthorized, string(ErrCodeUnauthorized), "Your session has expired. Please log in again.")
				return
			}
			claims, err := options.Verifier.Verify(c.Request.Context(), token)
			if err != nil {
				response.AbortErr(c, http.StatusUnauthorized, string(ErrCodeUnauthorized), "Your session has expired. Please log in again.")
				return
			}
			SetGinAuth(c, claims.ToAuthContext())
			c.Next()
			return
		}

		if guestCtx != nil {
			SetGinAuth(c, *guestCtx)
		}
		c.Next()
	}
}

func guestContextFromRequest(r *http.Request, store GuestStore, headerName string, cookieName string) (*AuthContext, error) {
	if r == nil || store == nil {
		return nil, nil
	}
	token := strings.TrimSpace(r.Header.Get(headerName))
	if token == "" {
		if cookie, err := r.Cookie(cookieName); err == nil {
			token = strings.TrimSpace(cookie.Value)
		}
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
	return AuthContext{
		UserID: userID,
		Role:   role,
		Scope:  scope,
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

package authverify

import "github.com/gin-gonic/gin"

// SetGinAuth stores the AuthContext on both the Gin context (fast in-handler
// lookup via the "auth" key) and the request context (so code holding only
// the *http.Request still sees it).
func SetGinAuth(c *gin.Context, authCtx AuthContext) {
	if c == nil {
		return
	}
	c.Set("auth", authCtx)
	c.Request = c.Request.WithContext(WithAuthContext(c.Request.Context(), authCtx))
}

// GetGinAuth reads the AuthContext set by SetGinAuth, falling back to the
// request context for requests that entered through non-Gin middleware.
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

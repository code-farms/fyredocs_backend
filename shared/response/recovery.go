package response

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
)

// GinRecovery recovers panics in Gin handlers, logs the stack server-side (never
// to the client), and returns the standard error envelope with a generic 500 —
// unlike gin.Recovery()/gin.Default(), which emit a bare, envelope-less 500.
func GinRecovery() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered any) {
		slog.ErrorContext(c.Request.Context(), "panic recovered",
			"error", fmt.Sprintf("%v", recovered), "op", "panic.recover", "stack", string(debug.Stack()))
		AbortErr(c, http.StatusInternalServerError, CodeServerError, "Something went wrong. Please try again.")
	})
}

// HTTPRecovery is the net/http equivalent for the api-gateway: it recovers a
// panic anywhere in the handler chain, logs the stack, and writes the standard
// 500 envelope instead of letting the connection drop with no response.
func HTTPRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.ErrorContext(r.Context(), "panic recovered",
					"error", fmt.Sprintf("%v", rec), "op", "panic.recover", "stack", string(debug.Stack()))
				WriteErr(w, http.StatusInternalServerError, CodeServerError, "Something went wrong. Please try again.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

package authverify

import (
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"fyredocs/shared/response"
)

// InternalTokenHeader is the shared-secret header guarding mesh-only /internal
// routes. Callers read INTERNAL_API_TOKEN from the environment and set it on
// every internal request; servers verify it with InternalTokenGin.
const InternalTokenHeader = "X-Internal-Token"

// InternalTokenGin returns middleware that requires X-Internal-Token to match
// the expected shared secret. It FAILS CLOSED: when expected is empty (the env
// var is unset), every internal request is refused with 503 rather than
// silently letting the mesh go unauthenticated — a missing variable must never
// reopen the hole. Comparison is constant-time over SHA-256 digests, which
// also hides the secret's length.
//
// The fail-closed posture here is the deliberate opposite of the rate limiters,
// which fail OPEN when Redis is unavailable. The asymmetry is intentional and
// follows from what each control protects: a limiter that fails closed would
// turn a Redis blip into a total outage, whereas an auth check that fails open
// would expose the internal API. Availability loses to authentication; it wins
// against throttling.
func InternalTokenGin(expected string) gin.HandlerFunc {
	if expected == "" {
		slog.Error("INTERNAL_API_TOKEN is not configured — /internal routes will refuse all requests (503) until it is set")
		return func(c *gin.Context) {
			response.AbortErr(c, http.StatusServiceUnavailable, response.CodeServiceUnavailable,
				"Internal API is not configured.")
		}
	}

	expectedSum := sha256.Sum256([]byte(expected))
	return func(c *gin.Context) {
		gotSum := sha256.Sum256([]byte(c.GetHeader(InternalTokenHeader)))
		if subtle.ConstantTimeCompare(gotSum[:], expectedSum[:]) != 1 {
			response.AbortErr(c, http.StatusUnauthorized, response.CodeUnauthorized,
				"Invalid internal token.")
			return
		}
		c.Next()
	}
}

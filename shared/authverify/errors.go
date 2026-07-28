package authverify

import (
	"net/http"

	"fyredocs/shared/response"
)

// ErrorCode is a stable machine-readable auth error code returned to clients.
type ErrorCode string

const (
	// ErrCodeUnauthorized means the caller is not identified — no token, or one
	// that cannot be trusted. The client's correct response is to log in.
	ErrCodeUnauthorized ErrorCode = "AUTH_UNAUTHORIZED"
	// ErrCodeForbidden means the caller IS identified but lacks permission.
	// Re-authenticating will not help, so clients must not treat this as a
	// prompt to refresh or re-login.
	ErrCodeForbidden ErrorCode = "AUTH_FORBIDDEN"
	// ErrCodeTokenExpired distinguishes a valid-but-expired token from an
	// otherwise-invalid one so the client can silently refresh instead of
	// forcing a re-login.
	ErrCodeTokenExpired ErrorCode = "AUTH_TOKEN_EXPIRED"
)

// WriteError writes a standardized error response to an http.ResponseWriter.
func WriteError(w http.ResponseWriter, status int, code ErrorCode, message string) {
	response.WriteErr(w, status, string(code), message)
}

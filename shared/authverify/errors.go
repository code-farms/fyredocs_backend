package authverify

import (
	"net/http"

	"fyredocs/shared/response"
)

// ErrorCode is a stable machine-readable auth error code returned to clients.
type ErrorCode string

const (
	ErrCodeUnauthorized ErrorCode = "AUTH_UNAUTHORIZED"
	ErrCodeForbidden    ErrorCode = "AUTH_FORBIDDEN"
	// ErrCodeTokenExpired distinguishes a valid-but-expired token from an
	// otherwise-invalid one so the client can silently refresh instead of
	// forcing a re-login.
	ErrCodeTokenExpired ErrorCode = "AUTH_TOKEN_EXPIRED"
)

// WriteError writes a standardized error response to an http.ResponseWriter.
func WriteError(w http.ResponseWriter, status int, code ErrorCode, message string) {
	response.WriteErr(w, status, string(code), message)
}

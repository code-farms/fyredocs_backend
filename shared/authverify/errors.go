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
)

// WriteError writes a standardized error response to an http.ResponseWriter.
func WriteError(w http.ResponseWriter, status int, code ErrorCode, message string) {
	response.WriteErr(w, status, string(code), message)
}

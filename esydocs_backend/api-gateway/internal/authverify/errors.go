package authverify

import (
	"net/http"

	"esydocs/shared/response"
)

type ErrorCode string

const (
	ErrCodeUnauthorized ErrorCode = "AUTH_UNAUTHORIZED"
	ErrCodeForbidden    ErrorCode = "AUTH_FORBIDDEN"
)

// WriteError writes a standardized error response to an http.ResponseWriter.
func WriteError(w http.ResponseWriter, status int, code ErrorCode, message string) {
	response.WriteErr(w, status, string(code), message)
}

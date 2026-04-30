package authverify

import (
	"net/http"

	"fyredocs/shared/response"
)

type ErrorCode string

const (
	ErrCodeUnauthorized ErrorCode = "AUTH_UNAUTHORIZED"
	ErrCodeForbidden    ErrorCode = "AUTH_FORBIDDEN"
)

func WriteError(w http.ResponseWriter, status int, code ErrorCode, message string) {
	response.WriteErr(w, status, string(code), message)
}

package auth

import (
	"encoding/json"
	"net/http"
)

type ErrorCode string

const (
	ErrCodeUnauthorized ErrorCode = "AUTH_UNAUTHORIZED"
	ErrCodeForbidden    ErrorCode = "AUTH_FORBIDDEN"
)

type ErrorResponse struct {
	Error AuthError `json:"error"`
}

type AuthError struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

func WriteError(w http.ResponseWriter, status int, code ErrorCode, message string) {
	if w == nil {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorResponse{
		Error: AuthError{
			Code:    code,
			Message: message,
		},
	})
}

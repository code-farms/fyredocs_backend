package response

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// WriteErr writes an error response to a raw http.ResponseWriter.
func WriteErr(w http.ResponseWriter, status int, code string, message string) {
	if w == nil {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(APIResponse{
		Success: false,
		Message: message,
		Data:    nil,
		Error:   &APIError{Code: code, Details: message},
	}); err != nil {
		slog.Warn("failed to encode error response", "error", err, "op", "response.write_err", "code", code)
	}
}

// WriteOK writes a success response to a raw http.ResponseWriter.
func WriteOK(w http.ResponseWriter, status int, message string, data interface{}) {
	if w == nil {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(APIResponse{
		Success: true,
		Message: message,
		Data:    data,
	}); err != nil {
		slog.Warn("failed to encode success response", "error", err, "op", "response.write_ok")
	}
}

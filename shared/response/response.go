// Package response defines the unified API response envelope and helpers for
// writing success and error responses consistently from both net/http and Gin
// handlers across every service.
package response

// APIResponse is the unified envelope for all API responses.
type APIResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data"`
	Error   *APIError   `json:"error"`
	Meta    *Meta       `json:"meta,omitempty"`
}

// APIError represents a structured error.
type APIError struct {
	Code    string `json:"code"`
	Details string `json:"details"`
}

// Meta holds optional pagination or request metadata.
type Meta struct {
	Page      int    `json:"page,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Total     int64  `json:"total,omitempty"`
	RequestID string `json:"requestId,omitempty"`
}

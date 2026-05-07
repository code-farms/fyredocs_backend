package response

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// OK sends a 200 success response with data.
func OK(c *gin.Context, message string, data interface{}) {
	c.JSON(http.StatusOK, APIResponse{
		Success: true,
		Message: message,
		Data:    data,
		Error:   nil,
		Meta:    extractMeta(c),
	})
}

// Created sends a 201 success response.
func Created(c *gin.Context, message string, data interface{}) {
	c.JSON(http.StatusCreated, APIResponse{
		Success: true,
		Message: message,
		Data:    data,
		Error:   nil,
		Meta:    extractMeta(c),
	})
}

// OKWithMeta sends a 200 success response with explicit meta.
func OKWithMeta(c *gin.Context, message string, data interface{}, meta *Meta) {
	if meta != nil {
		if reqID := requestIDFromContext(c); reqID != "" {
			meta.RequestID = reqID
		}
	}
	c.JSON(http.StatusOK, APIResponse{
		Success: true,
		Message: message,
		Data:    data,
		Error:   nil,
		Meta:    meta,
	})
}

// NoContent sends a 204 with no body.
func NoContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
}

// Err sends an error response. Does NOT call c.Abort().
func Err(c *gin.Context, status int, code string, message string) {
	c.JSON(status, APIResponse{
		Success: false,
		Message: message,
		Data:    nil,
		Error:   &APIError{Code: code, Details: message},
		Meta:    extractMeta(c),
	})
}

// AbortErr sends an error response and calls c.Abort().
func AbortErr(c *gin.Context, status int, code string, message string) {
	c.AbortWithStatusJSON(status, APIResponse{
		Success: false,
		Message: message,
		Data:    nil,
		Error:   &APIError{Code: code, Details: message},
		Meta:    extractMeta(c),
	})
}

// BadRequest is a convenience for 400 errors.
func BadRequest(c *gin.Context, code string, message string) {
	Err(c, http.StatusBadRequest, code, message)
}

// NotFound is a convenience for 404 errors.
func NotFound(c *gin.Context, code string, message string) {
	Err(c, http.StatusNotFound, code, message)
}

// InternalError is a convenience for 500 errors.
func InternalError(c *gin.Context, code string, message string) {
	Err(c, http.StatusInternalServerError, code, message)
}

// Unauthorized is a convenience for 401 errors.
func Unauthorized(c *gin.Context, code string, message string) {
	Err(c, http.StatusUnauthorized, code, message)
}

// Forbidden is a convenience for 403 errors.
func Forbidden(c *gin.Context, code string, message string) {
	Err(c, http.StatusForbidden, code, message)
}

// Errorf is like Err, but also emits a structured slog.Error with the supplied
// underlying err and request context. Use this whenever an err is in scope at a
// failure site so the cause is debuggable from logs alone. Pass nil err to
// behave exactly like Err (no log emitted).
//
// attrs are slog key/value pairs and are appended after the standard fields.
func Errorf(c *gin.Context, status int, code string, message string, err error, attrs ...any) {
	if err != nil {
		slog.Error(message, buildLogAttrs(c, status, code, err, attrs)...)
	}
	Err(c, status, code, message)
}

// InternalErrorf is a 500 convenience that logs err first.
func InternalErrorf(c *gin.Context, code string, message string, err error, attrs ...any) {
	Errorf(c, http.StatusInternalServerError, code, message, err, attrs...)
}

// AbortErrorf is the c.Abort variant of Errorf for use in middleware.
func AbortErrorf(c *gin.Context, status int, code string, message string, err error, attrs ...any) {
	if err != nil {
		slog.Error(message, buildLogAttrs(c, status, code, err, attrs)...)
	}
	AbortErr(c, status, code, message)
}

func buildLogAttrs(c *gin.Context, status int, code string, err error, extra []any) []any {
	attrs := []any{
		"err", err,
		"code", code,
		"status", status,
	}
	if c != nil && c.Request != nil {
		attrs = append(attrs, "method", c.Request.Method, "path", c.Request.URL.Path)
	}
	if reqID := requestIDFromContext(c); reqID != "" {
		attrs = append(attrs, "requestId", reqID)
	}
	return append(attrs, extra...)
}

func extractMeta(c *gin.Context) *Meta {
	reqID := requestIDFromContext(c)
	if reqID == "" {
		return nil
	}
	return &Meta{RequestID: reqID}
}

func requestIDFromContext(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if id, exists := c.Get("requestID"); exists {
		if s, ok := id.(string); ok {
			return s
		}
	}
	return ""
}

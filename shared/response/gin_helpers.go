package response

import (
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

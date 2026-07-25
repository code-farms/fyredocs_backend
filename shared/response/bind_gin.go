package response

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

// BindJSON binds the request body into dst and runs its `binding` validation
// tags. On failure it writes the canonical 400 INVALID_INPUT envelope — with
// per-field messages in error.details for validator failures — and returns
// false so the handler can simply `return`. This is the single funnel for
// request-body validation errors: every handler using it produces the same
// code and details shape.
func BindJSON(c *gin.Context, dst any) bool {
	if err := c.ShouldBindJSON(dst); err != nil {
		var vErrs validator.ValidationErrors
		if errors.As(err, &vErrs) {
			details := formatValidationErrors(vErrs)
			c.JSON(http.StatusBadRequest, APIResponse{
				Success: false,
				Message: "Some fields are missing or invalid.",
				Error:   &APIError{Code: CodeInvalidInput, Details: details},
				Meta:    extractMeta(c),
			})
			return false
		}
		Err(c, http.StatusBadRequest, CodeInvalidInput, "Invalid request body.")
		return false
	}
	return true
}

// formatValidationErrors flattens validator errors into a compact
// "field: problem; field: problem" string (APIError.Details is a string).
func formatValidationErrors(vErrs validator.ValidationErrors) string {
	parts := make([]string, 0, len(vErrs))
	for _, fe := range vErrs {
		parts = append(parts, fmt.Sprintf("%s: %s", fieldName(fe), fieldMessage(fe)))
	}
	return strings.Join(parts, "; ")
}

// fieldName lower-camels the struct field name so details read like the JSON
// payload the client sent (FullName → fullName).
func fieldName(fe validator.FieldError) string {
	name := fe.Field()
	if name == "" {
		return "body"
	}
	return strings.ToLower(name[:1]) + name[1:]
}

// fieldMessage renders a human-readable message for the common validation
// tags; unknown tags fall back to naming the failed rule.
func fieldMessage(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "is required"
	case "email":
		return "must be a valid email address"
	case "uuid", "uuid4":
		return "must be a valid UUID"
	case "min":
		if fe.Kind().String() == "string" {
			return fmt.Sprintf("must be at least %s characters", fe.Param())
		}
		return fmt.Sprintf("must contain at least %s items", fe.Param())
	case "max":
		if fe.Kind().String() == "string" {
			return fmt.Sprintf("must be at most %s characters", fe.Param())
		}
		return fmt.Sprintf("must contain at most %s items", fe.Param())
	case "gt":
		return fmt.Sprintf("must be greater than %s", fe.Param())
	case "gte":
		return fmt.Sprintf("must be at least %s", fe.Param())
	case "oneof":
		return fmt.Sprintf("must be one of: %s", strings.ReplaceAll(fe.Param(), " ", ", "))
	case "hexcolor":
		return "must be a hex color like #RRGGBB"
	default:
		return fmt.Sprintf("failed the %q rule", fe.Tag())
	}
}

package handlers

import (
	"testing"
)

func TestAuthUserIDNilContext(t *testing.T) {
	result := authUserID(nil)
	if result != nil {
		t.Error("expected nil for nil context")
	}
}

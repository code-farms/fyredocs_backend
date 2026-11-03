package llm

import (
	"context"
	"fmt"
	"testing"

	"fyredocs/shared/response"
)

func TestUnconfiguredIsAIUnavailable(t *testing.T) {
	_, err := Unconfigured{}.Stream(context.Background(), Request{}, nil)
	e, ok := AsError(err)
	if !ok || e.Code != response.CodeAIUnavailable {
		t.Fatalf("got %v", err)
	}
}

func TestAsErrorUnwraps(t *testing.T) {
	wrapped := fmt.Errorf("ctx: %w", &Error{Code: response.CodeAIRequestTooLarge, Message: "m"})
	if e, ok := AsError(wrapped); !ok || e.Code != response.CodeAIRequestTooLarge {
		t.Fatalf("got %v %v", e, ok)
	}
	if _, ok := AsError(fmt.Errorf("plain")); ok {
		t.Fatal("plain error classified")
	}
}

// Package llm is ai-service's model-provider seam. Handlers depend only on
// the Client interface; the concrete implementation speaks the
// OpenAI-compatible Chat Completions wire protocol, so the provider and the
// model are configuration (AI_BASE_URL / AI_MODEL_*), never code.
package llm

import (
	"context"
	"errors"

	"fyredocs/shared/response"
)

// Tier selects a model class. Ops name a tier, not a model, so swapping a
// model is an env change that cannot touch op routing.
type Tier string

// The two tiers ai-service routes to.
const (
	TierFast  Tier = "fast"
	TierSmart Tier = "smart"
)

// Message is one chat message. Role is "system", "user" or "assistant" —
// the only roles every OpenAI-compatible provider accepts. Images (user
// messages only) travel as the standard image_url content part.
type Message struct {
	Role    string
	Content string
	Images  []Image
}

// Image is one inline image, sent as a data URL.
type Image struct {
	// DataURL is "data:image/<jpeg|png|webp>;base64,<data>".
	DataURL string
}

// WithoutImages returns req with every image dropped — the text-only
// fallback for providers or models without vision.
func (r Request) WithoutImages() Request {
	msgs := make([]Message, len(r.Messages))
	for i, m := range r.Messages {
		m.Images = nil
		msgs[i] = m
	}
	r.Messages = msgs
	return r
}

// HasImages reports whether any message carries an image.
func (r Request) HasImages() bool {
	for _, m := range r.Messages {
		if len(m.Images) > 0 {
			return true
		}
	}
	return false
}

// Request is a provider-neutral completion request.
type Request struct {
	Tier      Tier
	Messages  []Message
	MaxTokens int
}

// Usage is the token accounting for one completion. CachedTokens is the
// prompt-prefix cache hit count when the provider reports it, else 0.
type Usage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	CachedTokens int `json:"cachedTokens"`
}

// Client streams a completion. onDelta receives text in order; returning an
// error from it aborts the stream (a disconnected client). The returned error
// is an *Error whenever the failure is the provider's, so callers can map it
// to a status and code without knowing the vendor.
type Client interface {
	Stream(ctx context.Context, req Request, onDelta func(text string) error) (Usage, error)
}

// Error is a provider failure already classified into an envelope code.
// Status is the provider's HTTP status when there was one (0 otherwise).
type Error struct {
	Code    string
	Message string
	Status  int
}

// Error implements error.
func (e *Error) Error() string { return e.Code + ": " + e.Message }

// AsError extracts an *Error from err's chain.
func AsError(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

// Unconfigured is the Client used when no provider is configured. Booting
// without one is allowed on purpose: the rest of the product must keep
// working, and the app shows a specific "AI unavailable" sentence instead of
// a broken service.
type Unconfigured struct{}

// Stream always fails with AI_UNAVAILABLE.
func (Unconfigured) Stream(context.Context, Request, func(string) error) (Usage, error) {
	return Usage{}, &Error{Code: response.CodeAIUnavailable, Message: "no AI provider is configured"}
}

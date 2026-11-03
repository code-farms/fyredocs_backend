package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"fyredocs/shared/authverify"
	"fyredocs/shared/queue"
	"fyredocs/shared/response"

	"ai-service/internal/llm"
	"ai-service/internal/ops"
	"ai-service/internal/sse"
)

// maxBodyBytes matches the gateway's per-proxy limit, so a direct (mesh)
// caller cannot send more than one routed through the gateway.
const maxBodyBytes = 1 << 20

// Publisher records an analytics event. Nil disables analytics (tests, or a
// boot without JetStream); a publish failure is logged and never fails the op.
type Publisher func(ctx context.Context, event queue.AnalyticsEvent) error

// Ops serves POST /api/ai/ops/:op.
type Ops struct {
	LLM       llm.Client
	Publish   Publisher
	MaxTokens int
}

// Event is one SSE frame. The shape is the mobile app's AiEvent
// (fyredocs_app/src/lib/ai/provider.ts) — change both together.
type Event struct {
	Type    string     `json:"type"`
	Text    string     `json:"text,omitempty"`
	Usage   *llm.Usage `json:"usage,omitempty"`
	Code    string     `json:"code,omitempty"`
	Message string     `json:"message,omitempty"`
}

// opResult is the JSON (non-streaming) response data.
type opResult struct {
	Text  string    `json:"text"`
	Usage llm.Usage `json:"usage"`
}

// RequireUser rejects guests and anonymous callers. AI is sign-in-only:
// the signed-in identity is what quota is charged to, so a guest request has
// nothing to bill and is refused before any input is read.
func RequireUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		auth, ok := authverify.GetGinAuth(c)
		if !ok || auth.IsGuest || strings.TrimSpace(auth.UserID) == "" {
			response.AbortErr(c, http.StatusUnauthorized, response.CodeUnauthorized, "Sign in to use AI features.")
			return
		}
		c.Next()
	}
}

// statusFor maps an llm.Error code to its HTTP status.
func statusFor(code string) int {
	switch code {
	case response.CodeAIRequestTooLarge:
		return http.StatusRequestEntityTooLarge
	case response.CodeAIQuotaExceeded:
		return http.StatusTooManyRequests
	default:
		return http.StatusServiceUnavailable
	}
}

// classify turns any Stream error into an envelope code + client message.
// Unclassified errors become AI_UNAVAILABLE: the cause is logged, never sent,
// because provider error bodies can echo request content or account details.
func classify(err error) (string, string) {
	if e, ok := llm.AsError(err); ok {
		switch e.Code {
		case response.CodeAIRequestTooLarge:
			return e.Code, "That request is too large for the AI model — try fewer pages."
		case response.CodeAIQuotaExceeded:
			return e.Code, "AI quota exceeded for this period."
		}
	}
	return response.CodeAIUnavailable, "The AI service is unavailable right now."
}

// Run validates the op input, builds the server-owned prompt and streams the
// answer. With Accept: text/event-stream the answer streams as SSE frames;
// otherwise it is collected into the standard envelope. A failure before the
// first frame is a normal JSON error with its status; after it, an SSE
// `error` frame (the 200 is already on the wire).
func (h *Ops) Run(c *gin.Context) {
	name := c.Param("op")
	spec, ok := ops.Lookup(name)
	if !ok {
		response.Err(c, http.StatusNotFound, response.CodeNotFound, "Unknown AI operation.")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
	var in ops.Input
	if err := json.NewDecoder(c.Request.Body).Decode(&in); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			response.Err(c, http.StatusRequestEntityTooLarge, response.CodeAIRequestTooLarge, "The request body is too large.")
			return
		}
		response.Err(c, http.StatusBadRequest, response.CodeInvalidInput, "Invalid request body.")
		return
	}
	if verr := ops.Validate(spec, name, in); verr != nil {
		response.Err(c, verr.Status, verr.Code, verr.Message)
		return
	}

	req := llm.Request{Tier: spec.Tier, Messages: ops.Build(spec, name, in), MaxTokens: h.MaxTokens}
	streaming := strings.Contains(c.GetHeader("Accept"), "text/event-stream")
	ctx := c.Request.Context()
	started := time.Now()

	var (
		usage llm.Usage
		err   error
		text  strings.Builder
	)
	if streaming {
		w := sse.New(c.Writer)
		usage, err = h.LLM.Stream(ctx, req, func(delta string) error {
			return w.Send(Event{Type: "delta", Text: delta})
		})
		if err != nil {
			code, msg := classify(err)
			h.logFailure(c, name, err, code)
			if !w.Started() {
				response.Err(c, statusFor(code), code, msg)
				return
			}
			_ = w.Send(Event{Type: "error", Code: code, Message: msg})
			return
		}
		_ = w.Send(Event{Type: "done", Usage: &usage})
	} else {
		usage, err = h.LLM.Stream(ctx, req, func(delta string) error {
			text.WriteString(delta)
			return nil
		})
		if err != nil {
			code, msg := classify(err)
			h.logFailure(c, name, err, code)
			response.Err(c, statusFor(code), code, msg)
			return
		}
		response.OK(c, "OK", opResult{Text: text.String(), Usage: usage})
	}
	h.recordCompleted(c, name, spec.Tier, usage, time.Since(started))
}

// logFailure logs a failed op; a client disconnect is info, not a warning.
func (h *Ops) logFailure(c *gin.Context, op string, err error, code string) {
	if c.Request.Context().Err() != nil {
		slog.InfoContext(c.Request.Context(), "ai op cancelled by client", "op", op)
		return
	}
	slog.WarnContext(c.Request.Context(), "ai op failed", "op", op, "code", code, "error", err)
}

// recordCompleted publishes the ai.op.completed analytics event — token
// counts only, never document text or answers.
func (h *Ops) recordCompleted(c *gin.Context, op string, tier llm.Tier, usage llm.Usage, took time.Duration) {
	if h.Publish == nil {
		return
	}
	auth, _ := authverify.GetGinAuth(c)
	meta, _ := json.Marshal(map[string]any{
		"tier":         tier,
		"inputTokens":  usage.InputTokens,
		"outputTokens": usage.OutputTokens,
		"cachedTokens": usage.CachedTokens,
		"durationMs":   took.Milliseconds(),
	})
	// Detached from the request context: the client may already have gone,
	// and the usage happened regardless.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 2*time.Second)
	defer cancel()
	if err := h.Publish(ctx, queue.AnalyticsEvent{
		EventType: "ai.op.completed",
		UserID:    auth.UserID,
		ToolType:  op,
		PlanName:  auth.Plan,
		Metadata:  meta,
		Timestamp: time.Now().UTC(),
	}); err != nil {
		slog.WarnContext(ctx, "ai analytics publish failed", "op", op, "error", err)
	}
}

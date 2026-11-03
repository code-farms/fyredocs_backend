package llm

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"fyredocs/shared/circuitbreaker"
	"fyredocs/shared/config"
	"fyredocs/shared/response"
)

// Config selects the provider. Every field is env-driven so switching vendor
// (Gemini, OpenAI, Groq, OpenRouter, Ollama, vLLM…) is an .env edit only.
type Config struct {
	BaseURL   string
	APIKey    string
	ModelFast string
	// ModelSmart falls back to ModelFast when unset — one-model setups work.
	ModelSmart string
	Timeout    time.Duration
	MaxRetries int
	// MaxTokensParam is "max_tokens" (most providers) or
	// "max_completion_tokens" (newer OpenAI reasoning models reject max_tokens).
	MaxTokensParam string
	// Vision allows image content parts. False (a text-only model) strips
	// images before sending; the request then runs on its text alone.
	Vision bool
}

// ConfigFromEnv reads AI_BASE_URL, AI_API_KEY, AI_MODEL_FAST, AI_MODEL_SMART,
// AI_REQUEST_TIMEOUT, AI_MAX_RETRIES and AI_MAX_TOKENS_PARAM.
func ConfigFromEnv() Config {
	timeout, err := time.ParseDuration(config.GetEnv("AI_REQUEST_TIMEOUT", "90s"))
	if err != nil || timeout <= 0 {
		timeout = 90 * time.Second
	}
	return Config{
		BaseURL:        strings.TrimSpace(config.GetEnv("AI_BASE_URL", "")),
		APIKey:         strings.TrimSpace(config.GetEnv("AI_API_KEY", "")),
		ModelFast:      strings.TrimSpace(config.GetEnv("AI_MODEL_FAST", "")),
		ModelSmart:     strings.TrimSpace(config.GetEnv("AI_MODEL_SMART", "")),
		Timeout:        timeout,
		MaxRetries:     config.GetEnvInt("AI_MAX_RETRIES", 1),
		MaxTokensParam: config.GetEnv("AI_MAX_TOKENS_PARAM", "max_tokens"),
		Vision:         config.GetEnvBool("AI_VISION", true),
	}
}

// New returns the OpenAI-compatible client for cfg, or Unconfigured when the
// endpoint or fast model is missing. The API key may be empty on purpose:
// local servers (Ollama, vLLM) accept unauthenticated requests.
func New(cfg Config) Client {
	if cfg.BaseURL == "" || cfg.ModelFast == "" {
		slog.Warn("AI provider not configured (AI_BASE_URL / AI_MODEL_FAST unset) — every op answers AI_UNAVAILABLE")
		return Unconfigured{}
	}
	if cfg.ModelSmart == "" {
		cfg.ModelSmart = cfg.ModelFast
	}
	c := openai.NewClient(
		option.WithBaseURL(cfg.BaseURL),
		option.WithAPIKey(cfg.APIKey),
		option.WithRequestTimeout(cfg.Timeout),
		option.WithMaxRetries(cfg.MaxRetries),
	)
	slog.Info("AI provider configured", "baseURL", cfg.BaseURL, "modelFast", cfg.ModelFast, "modelSmart", cfg.ModelSmart)
	return &Compat{cfg: cfg, client: c, breaker: circuitbreaker.NewTwoStep("ai-provider")}
}

// Compat speaks the OpenAI-compatible Chat Completions streaming protocol.
// Only portable features are used — messages, stream, a token cap, usage in
// the final chunk — so any compatible endpoint works unchanged.
type Compat struct {
	cfg     Config
	client  openai.Client
	breaker *circuitbreaker.TwoStep
}

// model resolves a tier to the configured model name.
func (c *Compat) model(t Tier) string {
	if t == TierSmart {
		return c.cfg.ModelSmart
	}
	return c.cfg.ModelFast
}

// params builds the wire request for req.
func (c *Compat) params(req Request) openai.ChatCompletionNewParams {
	msgs := make([]openai.ChatCompletionMessageParamUnion, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			msgs = append(msgs, openai.SystemMessage(m.Content))
		case "assistant":
			msgs = append(msgs, openai.ChatCompletionMessageParamUnion{OfAssistant: &openai.ChatCompletionAssistantMessageParam{
				Content: openai.ChatCompletionAssistantMessageParamContentUnion{OfString: openai.String(m.Content)},
			}})
		default:
			if len(m.Images) == 0 {
				msgs = append(msgs, openai.UserMessage(m.Content))
				continue
			}
			parts := []openai.ChatCompletionContentPartUnionParam{openai.TextContentPart(m.Content)}
			for _, img := range m.Images {
				parts = append(parts, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{URL: img.DataURL}))
			}
			msgs = append(msgs, openai.UserMessage(parts))
		}
	}
	p := openai.ChatCompletionNewParams{
		Model:         c.model(req.Tier),
		Messages:      msgs,
		StreamOptions: openai.ChatCompletionStreamOptionsParam{IncludeUsage: openai.Bool(true)},
	}
	if req.MaxTokens > 0 {
		if c.cfg.MaxTokensParam == "max_completion_tokens" {
			p.MaxCompletionTokens = openai.Int(int64(req.MaxTokens))
		} else {
			p.MaxTokens = openai.Int(int64(req.MaxTokens))
		}
	}
	return p
}

// Stream implements Client. The breaker counts only provider-side failures:
// a too-large input or a client disconnect says nothing about provider health.
func (c *Compat) Stream(ctx context.Context, req Request, onDelta func(string) error) (Usage, error) {
	done, err := c.breaker.Allow()
	if err != nil {
		return Usage{}, &Error{Code: response.CodeAIUnavailable, Message: "provider circuit open"}
	}
	if !c.cfg.Vision && req.HasImages() {
		req = req.WithoutImages()
	}
	usage, err := c.stream(ctx, req, onDelta)
	// A provider or model that rejects image parts answers 400; retry once on
	// the text alone rather than failing an extraction that text can serve.
	// Only before any output: a 400 always arrives before the first delta.
	if e, ok := AsError(err); ok && ctx.Err() == nil && req.HasImages() &&
		e.Status == http.StatusBadRequest && e.Code == response.CodeAIUnavailable {
		slog.WarnContext(ctx, "provider rejected image input — retrying text-only (set AI_VISION=false for this model)")
		usage, err = c.stream(ctx, req.WithoutImages(), onDelta)
	}
	if err != nil && ctx.Err() == nil {
		if e, ok := AsError(err); ok && e.Code == response.CodeAIUnavailable {
			done(err)
			return usage, err
		}
	}
	done(nil)
	return usage, err
}

// stream runs one streaming completion and classifies its failure.
func (c *Compat) stream(ctx context.Context, req Request, onDelta func(string) error) (Usage, error) {
	s := c.client.Chat.Completions.NewStreaming(ctx, c.params(req))
	defer s.Close()

	var (
		usage    Usage
		outChars int
	)
	for s.Next() {
		chunk := s.Current()
		if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
			usage = Usage{
				InputTokens:  int(chunk.Usage.PromptTokens),
				OutputTokens: int(chunk.Usage.CompletionTokens),
				CachedTokens: int(chunk.Usage.PromptTokensDetails.CachedTokens),
			}
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content == "" {
				continue
			}
			outChars += len([]rune(choice.Delta.Content))
			if err := onDelta(choice.Delta.Content); err != nil {
				return usage, err
			}
		}
	}
	if err := s.Err(); err != nil {
		if ctx.Err() != nil {
			return usage, ctx.Err()
		}
		return usage, classify(err)
	}
	// Some compatible providers ignore stream_options.include_usage; estimate
	// so the quota ledger never records a free answer.
	if usage.InputTokens == 0 && usage.OutputTokens == 0 {
		usage = Usage{InputTokens: EstimateTokens(req.Messages), OutputTokens: (outChars + 3) / 4}
	}
	return usage, nil
}

// classify maps a provider error to an envelope code by HTTP status and
// message wording only — never by vendor-specific error types — so it holds
// across every compatible provider.
func classify(err error) *Error {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		// The raw response too: some providers (Gemini's compat layer) return
		// an error shape the library cannot decode, leaving Message empty.
		// Matched only here, never forwarded to the client.
		msg := strings.ToLower(apiErr.Message + " " + string(apiErr.DumpResponse(true)))
		switch {
		case apiErr.StatusCode == http.StatusRequestEntityTooLarge,
			apiErr.StatusCode == http.StatusBadRequest && mentionsContextLimit(msg):
			return &Error{Code: response.CodeAIRequestTooLarge, Message: apiErr.Error(), Status: apiErr.StatusCode}
		default:
			return &Error{Code: response.CodeAIUnavailable, Message: apiErr.Error(), Status: apiErr.StatusCode}
		}
	}
	return &Error{Code: response.CodeAIUnavailable, Message: err.Error()}
}

// mentionsContextLimit recognises the context-window wording providers use
// ("maximum context length", "context window", "too many tokens", "input is too long").
func mentionsContextLimit(msg string) bool {
	for _, k := range []string{"context length", "context window", "context_length", "too many tokens", "too long", "maximum number of tokens", "token limit"} {
		if strings.Contains(msg, k) {
			return true
		}
	}
	return false
}

// ImageTokens is the flat per-image input estimate. Providers bill images by
// tile/resolution; ~1,500 covers a ≤1600px page on the common tilings, and
// reservation only needs an upper-ish bound — settle records the real usage.
const ImageTokens = 1500

// EstimateTokens is the ~4 chars/token heuristic over message content (plus
// ImageTokens per image), used for quota reservation and when a provider
// omits usage.
func EstimateTokens(msgs []Message) int {
	chars, images := 0, 0
	for _, m := range msgs {
		chars += len([]rune(m.Content)) + 4
		images += len(m.Images)
	}
	return (chars+3)/4 + images*ImageTokens
}

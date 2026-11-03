package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"fyredocs/shared/response"

	"ai-service/internal/llm"
	"ai-service/internal/transcribe"
)

// transcribeInput is the POST /api/ai/transcribe body.
type transcribeInput struct {
	Images   []string `json:"images"`
	Language string   `json:"language"`
}

// transcribeResult returns one text per image, plus usage.
type transcribeResult struct {
	Pages []string  `json:"pages"`
	Usage llm.Usage `json:"usage"`
}

// Transcribe reads handwriting (and any printed text) from 1–2 page images
// verbatim — the job tesseract cannot do. The image IS the input, so the
// request sets RequireImages: a text-only model answers AI_UNAVAILABLE
// rather than a transcription of nothing. Like Extract, the text is never
// logged, cached or put in analytics.
func (h *Ops) Transcribe(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, extractMaxBodyBytes)
	var in transcribeInput
	if err := json.NewDecoder(c.Request.Body).Decode(&in); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			response.Err(c, http.StatusRequestEntityTooLarge, response.CodeAIRequestTooLarge, "The request body is too large.")
			return
		}
		response.Err(c, http.StatusBadRequest, response.CodeInvalidInput, "Invalid request body.")
		return
	}
	if len(in.Images) == 0 {
		response.Err(c, http.StatusBadRequest, response.CodeInvalidInput, "Send 1 or 2 page images.")
		return
	}
	images, verr := validateImages(in.Images)
	if verr != nil {
		response.Err(c, verr.Status, verr.Code, verr.Message)
		return
	}
	lang := strings.ToLower(strings.TrimSpace(in.Language))
	if _, ok := transcribe.Languages[lang]; !ok {
		response.Err(c, http.StatusBadRequest, response.CodeInvalidInput, "language must be eng, hin or eng+hin.")
		return
	}

	req := llm.Request{Tier: llm.TierSmart, Messages: transcribe.Build(images, lang), MaxTokens: h.MaxTokens, RequireImages: true}
	started := time.Now()
	var (
		usage llm.Usage
		err   error
		reply strings.Builder
	)
	if h.Quota != nil {
		settle, ok := h.reserve(c, req)
		if !ok {
			return
		}
		defer func() { settle(chargeable(usage, err, req, reply.Len())) }()
	}
	usage, err = h.LLM.Stream(c.Request.Context(), req, func(d string) error {
		reply.WriteString(d)
		return nil
	})
	if err != nil {
		code, msg := classify(err)
		h.logFailure(c, "transcribe", err, code)
		response.Err(c, statusFor(code), code, msg)
		return
	}
	response.OK(c, "OK", transcribeResult{Pages: transcribe.SplitPages(reply.String(), len(images)), Usage: usage})
	h.recordCompleted(c, "transcribe", req.Tier, usage, time.Since(started), map[string]any{"pages": len(images)})
}

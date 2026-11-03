package handlers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"fyredocs/shared/response"

	"ai-service/internal/extract"
	"ai-service/internal/llm"
	"ai-service/internal/ops"
)

// Extract limits. Two ≤1.5 MB images plus text stay under the gateway's
// 4 MiB budget for this route once base64-inflated (≈4/3).
const (
	extractMaxBodyBytes = 4 << 20
	extractMaxImages    = 2
	extractMaxImageSize = 1_500_000
)

var dataURL = regexp.MustCompile(`^data:image/(jpeg|png|webp);base64,`)

// extractInput is the POST /api/ai/extract body.
type extractInput struct {
	Pages  []ops.Page `json:"pages"`
	Images []string   `json:"images"`
}

// extractResult adds usage to the typed extraction.
type extractResult struct {
	extract.Result
	Usage llm.Usage `json:"usage"`
}

// Extract classifies a document, suggests a title and extracts its typed
// fields in ONE call (cheaper than three ops). Input is page text, page
// images, or both; images are dropped automatically for text-only models.
//
// PII posture: extracted values (ID numbers, addresses…) are never logged,
// cached or put in analytics — the event carries the doc type only — and
// nothing is persisted server-side. The result goes back to the device.
func (h *Ops) Extract(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, extractMaxBodyBytes)
	var in extractInput
	if err := json.NewDecoder(c.Request.Body).Decode(&in); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			response.Err(c, http.StatusRequestEntityTooLarge, response.CodeAIRequestTooLarge, "The request body is too large.")
			return
		}
		response.Err(c, http.StatusBadRequest, response.CodeInvalidInput, "Invalid request body.")
		return
	}
	images, verr := validateImages(in.Images)
	if verr != nil {
		response.Err(c, verr.Status, verr.Code, verr.Message)
		return
	}
	if len(in.Pages) == 0 && len(images) == 0 {
		response.Err(c, http.StatusBadRequest, response.CodeInvalidInput, "Send page text, page images, or both.")
		return
	}
	if verr := ops.Validate(ops.Spec{}, "extract", ops.Input{Pages: in.Pages}); verr != nil {
		response.Err(c, verr.Status, verr.Code, verr.Message)
		return
	}

	req := llm.Request{Tier: llm.TierFast, Messages: extract.Build(in.Pages, images), MaxTokens: h.MaxTokens}
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
		h.logFailure(c, "extract", err, code)
		response.Err(c, statusFor(code), code, msg)
		return
	}
	result := extract.Parse(reply.String())
	response.OK(c, "OK", extractResult{Result: result, Usage: usage})
	h.recordCompleted(c, "extract", req.Tier, usage, time.Since(started), map[string]any{"docType": result.DocType})
}

// validateImages checks each data URL's type, base64 validity and size.
func validateImages(urls []string) ([]llm.Image, *ops.ValidationError) {
	if len(urls) > extractMaxImages {
		return nil, &ops.ValidationError{Status: http.StatusRequestEntityTooLarge, Code: response.CodeAIRequestTooLarge, Message: "At most 2 page images per request."}
	}
	out := make([]llm.Image, 0, len(urls))
	for _, u := range urls {
		loc := dataURL.FindStringIndex(u)
		if loc == nil {
			return nil, &ops.ValidationError{Status: http.StatusBadRequest, Code: response.CodeInvalidInput, Message: "Images must be JPEG, PNG or WebP data URLs."}
		}
		raw, err := base64.StdEncoding.DecodeString(u[loc[1]:])
		if err != nil {
			return nil, &ops.ValidationError{Status: http.StatusBadRequest, Code: response.CodeInvalidInput, Message: "An image is not valid base64."}
		}
		if len(raw) > extractMaxImageSize {
			return nil, &ops.ValidationError{Status: http.StatusRequestEntityTooLarge, Code: response.CodeAIRequestTooLarge, Message: "Each page image must be at most 1.5 MB — downscale it first."}
		}
		out = append(out, llm.Image{DataURL: u})
	}
	return out, nil
}

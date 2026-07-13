package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"fyredocs/shared/config"
	"fyredocs/shared/logger"
	"fyredocs/shared/response"
)

type detectEdgesRequest struct {
	UploadID string `json:"uploadId"`
}

// detectHTTPClient is shared across requests; overridable in tests via
// organizePdfBaseURL.
var detectHTTPClient = &http.Client{Timeout: 15 * time.Second}

func organizePdfBaseURL() string {
	return strings.TrimRight(config.GetEnv("ORGANIZE_PDF_URL", "http://organize-pdf:8084"), "/")
}

// DetectEdges gives the mobile scanner a document-quad suggestion for an
// uploaded photo. It peeks at the upload (consumeUpload without releaseUpload —
// the upload stays usable for the subsequent scan-to-pdf job) and relays
// detection to organize-pdf's internal endpoint, which owns the imaging logic.
func DetectEdges(c *gin.Context) {
	var req detectEdgesRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.UploadID) == "" {
		response.BadRequest(c, "INVALID_INPUT", "An uploadId is required.")
		return
	}
	uploadID := strings.TrimSpace(req.UploadID)

	// Peek at the upload: validates existence, size cap, and image MIME
	// sniff for scan-to-pdf. Deliberately no releaseUpload — the same
	// uploadId feeds job creation next.
	consumed, err := consumeUpload(c.Request.Context(), "scan-to-pdf", uploadID)
	if err != nil {
		response.Errorf(c, http.StatusBadRequest, "INVALID_INPUT", err.Error(), err,
			"op", "detect_edges.consume_upload", "uploadId", uploadID)
		return
	}

	// Edge detection is image-only; PDFs are legal scan inputs but have no
	// pixels to detect on.
	if strings.EqualFold(filepath.Ext(consumed.OriginalName), ".pdf") {
		response.BadRequest(c, "INVALID_INPUT", "Edge detection requires an image upload, not a PDF.")
		return
	}

	payload, err := json.Marshal(map[string]string{"key": consumed.Key})
	if err != nil {
		response.InternalErrorf(c, "SERVER_ERROR", "Failed to build detection request.", err,
			"op", "detect_edges.marshal", "uploadId", uploadID)
		return
	}

	upstreamURL := organizePdfBaseURL() + "/internal/v1/detect-edges"
	upstreamReq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamURL, bytes.NewReader(payload))
	if err != nil {
		response.InternalErrorf(c, "SERVER_ERROR", "Failed to build detection request.", err,
			"op", "detect_edges.new_request", "uploadId", uploadID)
		return
	}
	upstreamReq.Header.Set("Content-Type", "application/json")
	if token := config.GetEnv("INTERNAL_API_TOKEN", ""); token != "" {
		upstreamReq.Header.Set("X-Internal-Token", token)
	}

	resp, err := detectHTTPClient.Do(upstreamReq)
	if err != nil {
		logger.LogErr(c.Request.Context(), "detect_edges.upstream", err, "uploadId", uploadID)
		response.Err(c, http.StatusBadGateway, "DETECTION_UNAVAILABLE", "Edge detection is temporarily unavailable.")
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		logger.LogErr(c.Request.Context(), "detect_edges.read_upstream", err, "uploadId", uploadID)
		response.Err(c, http.StatusBadGateway, "DETECTION_UNAVAILABLE", "Edge detection is temporarily unavailable.")
		return
	}

	if resp.StatusCode >= 500 {
		logger.LogErr(c.Request.Context(), "detect_edges.upstream_5xx",
			fmt.Errorf("organize-pdf returned %d", resp.StatusCode), "uploadId", uploadID)
		response.Err(c, http.StatusBadGateway, "DETECTION_UNAVAILABLE", "Edge detection is temporarily unavailable.")
		return
	}

	if resp.StatusCode >= 400 {
		// Relay the upstream envelope's error with a 422 — the request was
		// well-formed but the image could not be processed.
		var upstream struct {
			Error struct {
				Code    string `json:"code"`
				Details string `json:"details"`
			} `json:"error"`
			Message string `json:"message"`
		}
		code, message := "DETECTION_FAILED", "The image could not be analyzed."
		if err := json.Unmarshal(body, &upstream); err == nil {
			if upstream.Error.Code != "" {
				code = upstream.Error.Code
			}
			if upstream.Error.Details != "" {
				message = upstream.Error.Details
			} else if upstream.Message != "" {
				message = upstream.Message
			}
		}
		response.Err(c, http.StatusUnprocessableEntity, code, message)
		return
	}

	// Success: relay the payload's data through our own envelope.
	var upstream struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &upstream); err != nil || len(upstream.Data) == 0 {
		logger.LogErr(c.Request.Context(), "detect_edges.bad_upstream_body", err, "uploadId", uploadID)
		response.Err(c, http.StatusBadGateway, "DETECTION_UNAVAILABLE", "Edge detection is temporarily unavailable.")
		return
	}
	response.OK(c, "Edges detected", upstream.Data)
}

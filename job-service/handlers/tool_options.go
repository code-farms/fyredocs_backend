package handlers

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Per-tool options validation. Historically options were an opaque
// passthrough; the scanner options (crop corners, rotation, page size,
// enhancement) are the first schema'd payload. Validation lives here at the
// public boundary — workers still re-clamp defensively on their side.

const maxScanPages = 50

var (
	scanLanguages = map[string]bool{"eng": true, "deu": true, "fra": true, "spa": true, "hin": true}
	scanPageSizes = map[string]bool{"": true, "auto": true, "a4": true, "letter": true, "id": true}
	scanEnhance   = map[string]bool{"": true, "none": true, "grayscale": true, "bw": true, "color-boost": true}
)

// scanOptionsPayload mirrors the scan-to-pdf options contract. job-service
// owns its own copy of the shape (no shared DTOs across services). Unknown
// fields are ignored for forward compatibility.
type scanOptionsPayload struct {
	OCR      *bool             `json:"ocr"`
	Language string            `json:"language"`
	PageSize string            `json:"pageSize"`
	Enhance  string            `json:"enhance"`
	Pages    []scanPagePayload `json:"pages"`
}

type scanPagePayload struct {
	Corners  *scanCornersPayload `json:"corners"`
	Rotation *int                `json:"rotation"`
}

type scanCornersPayload struct {
	TL *scanPointPayload `json:"tl"`
	TR *scanPointPayload `json:"tr"`
	BR *scanPointPayload `json:"br"`
	BL *scanPointPayload `json:"bl"`
}

type scanPointPayload struct {
	X *float64 `json:"x"`
	Y *float64 `json:"y"`
}

// validateToolOptions checks the options payload for tools with a defined
// schema. Empty options and tools without a schema pass through unchanged.
func validateToolOptions(toolType string, raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	switch toolType {
	case "scan-to-pdf":
		return validateScanOptions(raw)
	default:
		return nil
	}
}

func validateScanOptions(raw string) error {
	var opts scanOptionsPayload
	if err := json.Unmarshal([]byte(raw), &opts); err != nil {
		return fmt.Errorf("options must be a valid JSON object")
	}

	if lang := strings.ToLower(strings.TrimSpace(opts.Language)); lang != "" && !scanLanguages[lang] {
		return fmt.Errorf("unsupported OCR language %q — supported: eng, deu, fra, spa, hin", opts.Language)
	}
	if size := strings.ToLower(strings.TrimSpace(opts.PageSize)); !scanPageSizes[size] {
		return fmt.Errorf("unsupported pageSize %q — supported: auto, a4, letter, id", opts.PageSize)
	}
	if enhance := strings.ToLower(strings.TrimSpace(opts.Enhance)); !scanEnhance[enhance] {
		return fmt.Errorf("unsupported enhance mode %q — supported: none, grayscale, bw, color-boost", opts.Enhance)
	}

	if len(opts.Pages) > maxScanPages {
		return fmt.Errorf("too many page entries (%d) — maximum is %d", len(opts.Pages), maxScanPages)
	}

	for i, page := range opts.Pages {
		if page.Rotation != nil {
			switch *page.Rotation {
			case 0, 90, 180, 270:
			default:
				return fmt.Errorf("pages[%d].rotation must be 0, 90, 180, or 270", i)
			}
		}
		if page.Corners != nil {
			corners := map[string]*scanPointPayload{
				"tl": page.Corners.TL, "tr": page.Corners.TR,
				"br": page.Corners.BR, "bl": page.Corners.BL,
			}
			present := 0
			for _, p := range corners {
				if p != nil {
					present++
				}
			}
			if present != 4 {
				return fmt.Errorf("pages[%d].corners must include all four corners (tl, tr, br, bl) or be omitted", i)
			}
			for name, p := range corners {
				if p.X == nil || p.Y == nil {
					return fmt.Errorf("pages[%d].corners.%s must have numeric x and y", i, name)
				}
				if *p.X < 0 || *p.X > 1 || *p.Y < 0 || *p.Y > 1 {
					return fmt.Errorf("pages[%d].corners.%s must be normalized to [0, 1]", i, name)
				}
			}
		}
	}

	return nil
}

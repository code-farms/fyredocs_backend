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

// maxScanPages caps pages per scan-to-pdf job. Each page is a separate image
// that gets decoded and perspective-corrected in memory, so this bounds peak
// worker memory for one job rather than expressing a product limit.
const maxScanPages = 50

var (
	// scanLanguages is the OCR language allowlist, and a TRUST BOUNDARY rather
	// than a preference list: the value reaches a tesseract command line, and
	// tesseract can only satisfy languages whose trained-data packs are installed
	// in the optimize-pdf image. Adding an entry here without adding the pack to
	// that Dockerfile turns every request using it into a job failure.
	scanLanguages = map[string]bool{"eng": true, "deu": true, "fra": true, "spa": true, "hin": true}
	// scanPageSizes is the allowlist of output geometries the scan pipeline can lay
	// out. "" and "auto" both mean "infer from the pages themselves".
	scanPageSizes = map[string]bool{"": true, "auto": true, "a4": true, "letter": true, "id": true}
	// scanEnhance is the allowlist of image-enhancement modes implemented in the
	// imaging package. "" and "none" both mean no enhancement.
	scanEnhance = map[string]bool{"": true, "none": true, "grayscale": true, "bw": true, "color-boost": true}
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

// scanPagePayload is one page of a scan job: the crop quad and rotation the user
// chose for it. Both fields are pointers so "absent" is distinguishable from
// "explicitly zero" — a rotation of 0 is meaningful, and a missing corners block
// means "use the whole image".
type scanPagePayload struct {
	Corners  *scanCornersPayload `json:"corners"`
	Rotation *int                `json:"rotation"`
}

// scanCornersPayload is the perspective-crop quad for one page, in the corner
// order the imaging code expects. Coordinates are NORMALIZED to 0..1 against the
// decoded pixel grid rather than device pixels, so the quad stays valid if the
// client resized or re-encoded the image before upload.
type scanCornersPayload struct {
	TL *scanPointPayload `json:"tl"`
	TR *scanPointPayload `json:"tr"`
	BR *scanPointPayload `json:"br"`
	BL *scanPointPayload `json:"bl"`
}

// scanPointPayload is a single normalized 0..1 coordinate pair.
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

// validateScanOptions is the hard validation gate for scan-to-pdf options.
//
// Unlike parseOptions in jobs.go, this rejects instead of defaulting, because
// every field here either bounds resource use (page count) or feeds a subprocess
// argument (language). Anything off an allowlist is refused at the public
// boundary so it can never reach a worker command line. Workers re-clamp anyway;
// that is defence in depth, not a reason to relax this.
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

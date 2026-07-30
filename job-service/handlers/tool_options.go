package handlers

import (
	"encoding/json"
	"fmt"
	"strconv"
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
	// scanMargins is the allowlist of page-margin presets. The worker maps each
	// to a pdfcpu import scale; margins only take effect with a fixed pageSize
	// (a4/letter/id) — with "auto" the worker accepts and ignores the value.
	scanMargins = map[string]bool{"": true, "none": true, "small": true, "normal": true, "large": true}
)

// maxMetadataFieldLen caps each pdf-metadata Info-dict entry (bytes,
// post-trim). The values land verbatim inside the output PDF; the cap bounds
// document bloat from a hostile payload rather than any pdfcpu limit.
const maxMetadataFieldLen = 512

// scanOptionsPayload mirrors the scan-to-pdf options contract. job-service
// owns its own copy of the shape (no shared DTOs across services). Unknown
// fields are ignored for forward compatibility.
type scanOptionsPayload struct {
	OCR      *bool             `json:"ocr"`
	Language string            `json:"language"`
	PageSize string            `json:"pageSize"`
	Enhance  string            `json:"enhance"`
	Margin   string            `json:"margin"`
	Pages    []scanPagePayload `json:"pages"`
}

// metadataOptionsPayload mirrors the pdf-metadata options contract (the
// worker's copy lives in organize-pdf/processing/processing_metadata.go —
// duplicated by design, no shared DTOs across services). Pointers so "absent"
// is distinguishable from "explicitly empty".
type metadataOptionsPayload struct {
	Title    *string `json:"title"`
	Author   *string `json:"author"`
	Subject  *string `json:"subject"`
	Keywords *string `json:"keywords"`
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
// schema. Empty options and tools without a schema pass through unchanged —
// except pdf-metadata, whose entire purpose is its options: absent options
// would otherwise fail only inside the worker, after the upload is consumed.
func validateToolOptions(toolType string, raw string) error {
	if strings.TrimSpace(raw) == "" {
		if toolType == "pdf-metadata" {
			return fmt.Errorf("pdf-metadata requires options with at least one of: title, author, subject, keywords")
		}
		return nil
	}
	switch toolType {
	case "scan-to-pdf":
		return validateScanOptions(raw)
	case "pdf-metadata":
		return validateMetadataOptions(raw)
	case "ocr-pdf":
		return validateOcrOptions(raw)
	case "pdf-to-image":
		// normalizeToolType folds the pdf-to-img alias before this runs.
		return validatePdfToImageOptions(raw)
	default:
		return nil
	}
}

var (
	// pdfImageFormats is the pdf-to-image format allowlist — a TRUST BOUNDARY:
	// the value selects a pdftoppm flag on the worker command line. The worker
	// keeps its own copy (convert-from-pdf/processing/processing_free.go);
	// duplicated by design, keep in sync.
	pdfImageFormats = map[string]bool{"": true, "png": true, "jpg": true, "jpeg": true, "tiff": true}
)

// Render-DPI bounds for pdf-to-image; same rationale as the OCR bounds (below
// 72 output is unusable, above 600 pages rasterize into hundreds of MB —
// uncompressed TIFF being the worst case).
const (
	minPdfImageDpi = 72
	maxPdfImageDpi = 600
)

// pdfToImageOptionsPayload mirrors the pdf-to-image options contract. Dpi
// stays raw because clients send both 300 and "300".
type pdfToImageOptionsPayload struct {
	Format string          `json:"format"`
	Dpi    json.RawMessage `json:"dpi"`
}

// validatePdfToImageOptions is the hard gate for pdf-to-image options:
// format feeds a subprocess flag (allowlist), dpi bounds worker memory.
func validatePdfToImageOptions(raw string) error {
	var opts pdfToImageOptionsPayload
	if err := json.Unmarshal([]byte(raw), &opts); err != nil {
		return fmt.Errorf("options must be a valid JSON object")
	}
	if !pdfImageFormats[strings.ToLower(strings.TrimSpace(opts.Format))] {
		return fmt.Errorf("unsupported image format %q — supported: png, jpg, tiff", opts.Format)
	}
	if dpiStr := strings.Trim(strings.TrimSpace(string(opts.Dpi)), `"`); dpiStr != "" && dpiStr != "null" {
		dpi, err := strconv.Atoi(dpiStr)
		if err != nil {
			return fmt.Errorf("dpi must be a whole number")
		}
		if dpi < minPdfImageDpi || dpi > maxPdfImageDpi {
			return fmt.Errorf("dpi must be between %d and %d", minPdfImageDpi, maxPdfImageDpi)
		}
	}
	return nil
}

var (
	// ocrLanguages is the ocr-pdf language allowlist — a TRUST BOUNDARY like
	// scanLanguages: the value reaches a tesseract command line, and only packs
	// installed in the optimize-pdf image can be satisfied. That image ships
	// English ONLY today; extend this set together with its Dockerfile when
	// more packs land. The worker's own wider map would silently degrade
	// unknown languages to eng — rejecting here keeps the contract honest.
	ocrLanguages = map[string]bool{"": true, "en": true, "eng": true}
)

// OCR render-DPI bounds. Below 72 tesseract accuracy collapses; above 600 a
// single page rasterizes into hundreds of MB. The worker additionally clamps
// DPI down against page dimensions — this is the coarse public-boundary gate.
const (
	minOcrDpi = 72
	maxOcrDpi = 600
)

// ocrOptionsPayload mirrors the ocr-pdf options contract (worker copy reads
// the same keys in optimize-pdf/processing/processing_free.go — duplicated by
// design, no shared DTOs). Dpi stays raw because clients send both 300 and
// "300" (the worker's optionString coerces the same way).
type ocrOptionsPayload struct {
	Language string          `json:"language"`
	Dpi      json.RawMessage `json:"dpi"`
}

// validateOcrOptions is the hard gate for ocr-pdf options: language feeds a
// subprocess argument (allowlist), dpi bounds worker memory (range check).
func validateOcrOptions(raw string) error {
	var opts ocrOptionsPayload
	if err := json.Unmarshal([]byte(raw), &opts); err != nil {
		return fmt.Errorf("options must be a valid JSON object")
	}
	if !ocrLanguages[strings.ToLower(strings.TrimSpace(opts.Language))] {
		return fmt.Errorf("unsupported OCR language %q — supported: eng", opts.Language)
	}
	if dpiStr := strings.Trim(strings.TrimSpace(string(opts.Dpi)), `"`); dpiStr != "" && dpiStr != "null" {
		dpi, err := strconv.Atoi(dpiStr)
		if err != nil {
			return fmt.Errorf("dpi must be a whole number")
		}
		if dpi < minOcrDpi || dpi > maxOcrDpi {
			return fmt.Errorf("dpi must be between %d and %d", minOcrDpi, maxOcrDpi)
		}
	}
	return nil
}

// validateMetadataOptions is the hard gate for pdf-metadata options: the
// values are written verbatim into the output PDF's Info dictionary, so this
// bounds size and rejects NUL bytes (which PDF string literals cannot carry
// safely through every reader) at the public boundary.
func validateMetadataOptions(raw string) error {
	var opts metadataOptionsPayload
	if err := json.Unmarshal([]byte(raw), &opts); err != nil {
		return fmt.Errorf("options must be a valid JSON object")
	}

	fields := map[string]*string{
		"title":    opts.Title,
		"author":   opts.Author,
		"subject":  opts.Subject,
		"keywords": opts.Keywords,
	}
	nonEmpty := 0
	for name, value := range fields {
		if value == nil {
			continue
		}
		trimmed := strings.TrimSpace(*value)
		if trimmed == "" {
			continue
		}
		if len(trimmed) > maxMetadataFieldLen {
			return fmt.Errorf("%s exceeds the maximum length of %d characters", name, maxMetadataFieldLen)
		}
		if strings.ContainsRune(trimmed, '\x00') {
			return fmt.Errorf("%s contains invalid characters", name)
		}
		nonEmpty++
	}
	if nonEmpty == 0 {
		return fmt.Errorf("pdf-metadata requires options with at least one of: title, author, subject, keywords")
	}
	return nil
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
	if margin := strings.ToLower(strings.TrimSpace(opts.Margin)); !scanMargins[margin] {
		return fmt.Errorf("unsupported margin %q — supported: none, small, normal, large", opts.Margin)
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

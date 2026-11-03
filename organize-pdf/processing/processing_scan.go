package processing

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"

	"organize-pdf/internal/imaging"
)

// Scan-to-pdf option handling and image preprocessing (perspective warp,
// rotation, enhancement, page sizing). Options arrive validated by
// job-service, but the worker re-validates defensively — invalid values
// degrade to safe defaults with a warning instead of failing the job.

// idCardAspect is the ISO/IEC 7810 ID-1 card aspect ratio (85.6mm × 54mm).
const idCardAspect = 85.6 / 54.0

// businessCardAspect is the US/international 3.5 × 2 in business card.
const businessCardAspect = 88.9 / 50.8

// scanLanguages are the tesseract language packs installed in the Dockerfile.
var scanLanguages = map[string]bool{
	"eng": true, "deu": true, "fra": true, "spa": true, "hin": true,
}

// ScanCorners is the normalized document quad for one page. All four corners
// must be present for the page to be warped.
type ScanCorners struct {
	TL *imaging.Point `json:"tl"`
	TR *imaging.Point `json:"tr"`
	BR *imaging.Point `json:"br"`
	BL *imaging.Point `json:"bl"`
}

// complete reports whether all four corners are present. Nil-safe on the
// receiver, because corners are absent entirely for a page the user did not crop.
// A partially specified quad is treated as unusable rather than interpolated —
// three corners cannot define the intended rectangle.
func (c *ScanCorners) complete() bool {
	return c != nil && c.TL != nil && c.TR != nil && c.BR != nil && c.BL != nil
}

// quad converts the wire form to an imaging.Quad. It dereferences all four
// pointers, so callers MUST check complete() first or this panics.
func (c *ScanCorners) quad() imaging.Quad {
	return imaging.Quad{TL: *c.TL, TR: *c.TR, BR: *c.BR, BL: *c.BL}
}

// ScanEdgeMids curves the crop: the normalized midpoints of the top, right,
// bottom and left edges (Phase 23 §4 dewarp). Used only together with a
// complete corner quad; all four must be present.
// NOTE: mirrored as scanEdgeMidsPayload in job-service/handlers/tool_options.go
// — duplicated by design, no shared DTOs across services.
type ScanEdgeMids struct {
	Top    *imaging.Point `json:"top"`
	Right  *imaging.Point `json:"right"`
	Bottom *imaging.Point `json:"bottom"`
	Left   *imaging.Point `json:"left"`
}

// complete reports whether all four midpoints are present (nil-safe).
func (m *ScanEdgeMids) complete() bool {
	return m != nil && m.Top != nil && m.Right != nil && m.Bottom != nil && m.Left != nil
}

// mids converts the wire form, clamped to 0..1; callers check complete() first.
func (m *ScanEdgeMids) mids() imaging.EdgeMids {
	c := func(p imaging.Point) imaging.Point {
		return imaging.Point{X: math.Max(0, math.Min(1, p.X)), Y: math.Max(0, math.Min(1, p.Y))}
	}
	return imaging.EdgeMids{Top: c(*m.Top), Right: c(*m.Right), Bottom: c(*m.Bottom), Left: c(*m.Left)}
}

// ScanCleanup selects classical clean-up passes for one page.
// NOTE: mirrored as scanCleanupPayload in job-service (duplicated by design).
type ScanCleanup struct {
	Shadows    bool `json:"shadows"`
	Background bool `json:"background"`
	Glare      bool `json:"glare"`
	Fingers    bool `json:"fingers"`
	Holes      bool `json:"holes"`
	Auto       bool `json:"auto"`
}

// options converts to the imaging package's option set (nil-safe).
func (c *ScanCleanup) options() imaging.CleanupOptions {
	if c == nil {
		return imaging.CleanupOptions{}
	}
	return imaging.CleanupOptions{
		Shadows: c.Shadows, Background: c.Background, Glare: c.Glare,
		Fingers: c.Fingers, Holes: c.Holes, Auto: c.Auto,
	}
}

// ScanPage carries per-page processing directives, index-aligned with the
// job's input files.
type ScanPage struct {
	Corners  *ScanCorners  `json:"corners,omitempty"`
	Rotation int           `json:"rotation,omitempty"`
	EdgeMids *ScanEdgeMids `json:"edgeMids,omitempty"`
	Cleanup  *ScanCleanup  `json:"cleanup,omitempty"`
}

// needsWork reports whether the page requires decoding at all (nil-safe).
func (p *ScanPage) needsWork() bool {
	return p != nil && (p.Corners.complete() || p.Rotation != 0 || p.Cleanup.options().Any())
}

// ScanWarning is a per-page note returned in job metadata ("warnings"), for
// outcomes the user should act on — e.g. glare that blew out text, which no
// clean-up can recover.
type ScanWarning struct {
	Page int    `json:"page"` // 1-based
	Code string `json:"code"` // "glare"
}

// ScanOptions is the full scan-to-pdf option payload.
type ScanOptions struct {
	OCR      bool       `json:"ocr"`
	Language string     `json:"language"`
	PageSize string     `json:"pageSize"` // "", auto, a4, letter, id, card
	Enhance  string     `json:"enhance"`  // "", none, grayscale, bw, color-boost
	Margin   string     `json:"margin"`   // "", none, small, normal, large
	Pages    []ScanPage `json:"pages"`
}

// parseScanOptions converts the untyped options map into ScanOptions,
// defensively clamping every field. The worker never fails a job over a bad
// option value — it degrades to the default and logs a warning.
func parseScanOptions(options map[string]interface{}) ScanOptions {
	var opts ScanOptions
	if len(options) > 0 {
		if raw, err := json.Marshal(options); err == nil {
			if err := json.Unmarshal(raw, &opts); err != nil {
				slog.Warn("scan options did not match schema; using defaults", "error", err)
				opts = ScanOptions{}
				// Preserve the one legacy option on schema mismatch.
				if ocr, ok := options["ocr"].(bool); ok {
					opts.OCR = ocr
				}
			}
		}
	}

	opts.Language = strings.ToLower(strings.TrimSpace(opts.Language))
	if opts.Language != "" && !scanLanguages[opts.Language] {
		slog.Warn("unsupported OCR language; falling back to eng", "language", opts.Language)
		opts.Language = ""
	}

	opts.PageSize = strings.ToLower(strings.TrimSpace(opts.PageSize))
	switch opts.PageSize {
	case "", "auto", "a4", "letter", "id", "card":
	default:
		slog.Warn("unknown pageSize; falling back to auto", "pageSize", opts.PageSize)
		opts.PageSize = ""
	}

	opts.Enhance = strings.ToLower(strings.TrimSpace(opts.Enhance))
	switch opts.Enhance {
	case "", "none", "grayscale", "bw", "color-boost":
	default:
		slog.Warn("unknown enhance mode; skipping enhancement", "enhance", opts.Enhance)
		opts.Enhance = ""
	}

	opts.Margin = strings.ToLower(strings.TrimSpace(opts.Margin))
	switch opts.Margin {
	case "", "none", "small", "normal", "large":
	default:
		slog.Warn("unknown margin; ignoring", "margin", opts.Margin)
		opts.Margin = ""
	}
	// Margins are implemented as an import scale on a FIXED page form; with
	// auto sizing the page IS the image and there is nothing to inset against
	// (pdfcpu defaults any non-empty descriptor to A4). Degrade, don't fail.
	if opts.Margin != "" && opts.Margin != "none" && (opts.PageSize == "" || opts.PageSize == "auto") {
		slog.Warn("margin requires a fixed pageSize (a4/letter/id/card); ignoring", "margin", opts.Margin)
		opts.Margin = ""
	}

	for i := range opts.Pages {
		p := &opts.Pages[i]
		switch p.Rotation {
		case 0, 90, 180, 270:
		default:
			slog.Warn("invalid page rotation; ignoring", "page", i, "rotation", p.Rotation)
			p.Rotation = 0
		}
		if p.Corners != nil {
			if !p.Corners.complete() {
				slog.Warn("partial corners; ignoring page crop", "page", i)
				p.Corners = nil
			} else {
				q := p.Corners.quad().Clamp01()
				p.Corners = &ScanCorners{TL: &q.TL, TR: &q.TR, BR: &q.BR, BL: &q.BL}
			}
		}
	}

	return opts
}

// tesseractLang maps the requested OCR language onto an installed pack.
func tesseractLang(requested string) string {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if scanLanguages[requested] {
		return requested
	}
	return "eng"
}

// needsPreprocessing reports whether any pixel work is required before the
// images can be embedded.
func needsPreprocessing(opts ScanOptions) bool {
	if opts.Enhance != "" && opts.Enhance != "none" {
		return true
	}
	for i := range opts.Pages {
		if opts.Pages[i].needsWork() {
			return true
		}
	}
	return false
}

// pageAspect returns the fixed output aspect (w/h) for a page size, or 0 to
// derive the aspect from the crop quad itself. Only the ID card imposes an
// aspect on the warp — A4/Letter are handled by pdfcpu page geometry.
// Cards (ID and business) impose one: their warp must land on the card's
// shape, not on whatever the photographed quad happened to be.
func pageAspect(pageSize string) float64 {
	switch pageSize {
	case "id":
		return idCardAspect
	case "card":
		return businessCardAspect
	}
	return 0
}

// processOnePage applies crop/warp (planar, or curved when edge midpoints
// are given), rotation, clean-up, and enhancement to a decoded image, in
// that order: clean-up runs on the final page geometry (fingers and holes
// are detected relative to the page's border and margins). Returns the
// processed image, whether it is bilevel (bw), and the clean-up report.
func processOnePage(img image.Image, page *ScanPage, enhance string, aspect float64) (image.Image, bool, imaging.CleanupReport, error) {
	out := img
	var report imaging.CleanupReport

	if page != nil && page.Corners.complete() {
		b := out.Bounds()
		quadNorm := page.Corners.quad().Clamp01()
		if err := quadNorm.Validate(); err != nil {
			slog.Warn("invalid crop quad; skipping warp", "error", err)
		} else if page.EdgeMids.complete() {
			quadPx := quadNorm.Denormalize(b.Dx(), b.Dy())
			mids := page.EdgeMids.mids().Denormalize(b.Dx(), b.Dy())
			w, h := imaging.CoonsOutputSize(quadPx, mids, aspect)
			warped, err := imaging.CoonsWarp(out, quadPx, mids, w, h)
			if err != nil {
				return nil, false, report, fmt.Errorf("curved warp: %w", err)
			}
			out = warped
		} else {
			quadPx := quadNorm.Denormalize(b.Dx(), b.Dy())
			w, h := imaging.WarpOutputSize(quadPx, aspect)
			warped, err := imaging.WarpPerspective(out, quadPx, w, h)
			if err != nil {
				return nil, false, report, fmt.Errorf("perspective warp: %w", err)
			}
			out = warped
		}
	}

	if page != nil && page.Rotation != 0 {
		out = imaging.Rotate(out, page.Rotation)
	}

	if opts := page.cleanupOptions(); opts.Any() {
		out, report = imaging.Cleanup(out, opts)
	}

	isBilevel := false
	switch enhance {
	case "grayscale":
		out = imaging.Grayscale(out)
	case "bw":
		out = imaging.AdaptiveThreshold(out, 0, 0.15)
		isBilevel = true
	case "color-boost":
		out = imaging.ColorBoost(out, 1.15, 1.25)
	}

	return out, isBilevel, report, nil
}

// cleanupOptions is the page's clean-up selection (nil-safe).
func (p *ScanPage) cleanupOptions() imaging.CleanupOptions {
	if p == nil {
		return imaging.CleanupOptions{}
	}
	return p.Cleanup.options()
}

// preprocessScanImages runs the per-page pipeline over every input image,
// writing processed copies into scratchDir. PDF inputs pass through
// untouched (they are legal scan-to-pdf inputs). The returned slice is
// index-aligned with inputPaths.
func preprocessScanImages(ctx context.Context, inputPaths []string, opts ScanOptions, scratchDir string) ([]string, []ScanWarning, error) {
	processed := make([]string, len(inputPaths))
	var warnings []ScanWarning
	for i, inputPath := range inputPaths {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}

		if strings.EqualFold(filepath.Ext(inputPath), ".pdf") {
			processed[i] = inputPath
			continue
		}

		var page *ScanPage
		if i < len(opts.Pages) {
			page = &opts.Pages[i]
		}

		// Skip decode entirely when this page needs no pixel work.
		pageNeedsWork := (opts.Enhance != "" && opts.Enhance != "none") || page.needsWork()
		if !pageNeedsWork {
			processed[i] = inputPath
			continue
		}

		img, _, err := imaging.DecodeFile(inputPath)
		if err != nil {
			return nil, nil, fmt.Errorf("decode %s: %w", filepath.Base(inputPath), err)
		}

		out, isBilevel, report, err := processOnePage(img, page, opts.Enhance, pageAspect(opts.PageSize))
		if err != nil {
			return nil, nil, fmt.Errorf("process %s: %w", filepath.Base(inputPath), err)
		}
		if report.Glare > 0 {
			warnings = append(warnings, ScanWarning{Page: i + 1, Code: "glare"})
		}

		base := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
		if isBilevel {
			outPath := filepath.Join(scratchDir, fmt.Sprintf("scan_%03d_%s.png", i, base))
			if err := imaging.EncodePNG(outPath, out); err != nil {
				return nil, nil, err
			}
			processed[i] = outPath
		} else {
			outPath := filepath.Join(scratchDir, fmt.Sprintf("scan_%03d_%s.jpg", i, base))
			if err := imaging.EncodeJPEG(outPath, out, 92); err != nil {
				return nil, nil, err
			}
			processed[i] = outPath
		}
	}
	return processed, warnings, nil
}

// marginScale maps the margin preset to a pdfcpu relative-scale token: the
// image is centered on the page form, so scaling it down leaves an even
// border. NOTE: the preset names are validated in
// job-service/handlers/tool_options.go — keep both lists in sync.
func marginScale(margin string) string {
	switch strings.ToLower(strings.TrimSpace(margin)) {
	case "small":
		return "0.95"
	case "normal":
		return "0.9"
	case "large":
		return "0.85"
	default:
		return "1.0"
	}
}

// importConfigForPageSize builds the pdfcpu import config for the requested
// output page size and margin preset. auto/empty keeps the current behavior
// (page = image dimensions; margin cannot apply — see parseScanOptions).
// Unknown values degrade to auto — job-service already rejected them for
// external callers.
func importConfigForPageSize(pageSize, margin string) (*pdfcpu.Import, error) {
	scale := marginScale(margin)
	switch strings.ToLower(strings.TrimSpace(pageSize)) {
	case "", "auto":
		return nil, nil
	case "a4":
		return api.Import(fmt.Sprintf("form:A4, pos:c, sc:%s rel", scale), types.POINTS)
	case "letter":
		return api.Import(fmt.Sprintf("form:Letter, pos:c, sc:%s rel", scale), types.POINTS)
	case "id":
		// ISO ID-1 card: 85.6 × 54 mm = 242.6 × 153.1 points.
		return api.Import(fmt.Sprintf("dim:243 153, pos:c, sc:%s rel", scale), types.POINTS)
	case "card":
		// Business card: 3.5 × 2 in = 252 × 144 points.
		return api.Import(fmt.Sprintf("dim:252 144, pos:c, sc:%s rel", scale), types.POINTS)
	default:
		return nil, nil
	}
}

// scanToPDF turns captured photos into a PDF: per page, apply the user's
// perspective crop and rotation, optionally enhance, then lay the results out at
// the requested page size.
//
// Corner coordinates arrive normalized to 0..1 against the DECODED pixel grid,
// which is why the client bakes EXIF orientation into pixels before upload — this
// code ignores EXIF, so a rotation left in metadata would misalign every crop.
// Pages without a complete corner set are used whole rather than skipped.
// The returned metadata is nil unless OCR ran (extractedText sidecar).
func scanToPDF(ctx context.Context, inputPaths []string, outputPath string, options map[string]interface{}) (map[string]interface{}, error) {
	slog.Info("converting images to PDF (scan-to-pdf)", "count", len(inputPaths))

	if len(inputPaths) == 0 {
		return nil, fmt.Errorf("no input images provided")
	}

	opts := parseScanOptions(options)

	paths := inputPaths
	if needsPreprocessing(opts) {
		scratchDir, err := os.MkdirTemp("", "pdf-scan-*")
		if err != nil {
			return nil, fmt.Errorf("failed to create scratch dir: %w", err)
		}
		defer os.RemoveAll(scratchDir)

		var warnings []ScanWarning
		paths, warnings, err = preprocessScanImages(ctx, inputPaths, opts, scratchDir)
		if err != nil {
			return nil, fmt.Errorf("scan preprocessing: %w", err)
		}

		if opts.OCR {
			// OCR must run inside this scope while scratch files still exist.
			meta, err := scanToPDFWithOCR(ctx, paths, outputPath, tesseractLang(opts.Language))
			return withWarnings(meta, warnings), err
		}

		imp, err := importConfigForPageSize(opts.PageSize, opts.Margin)
		if err != nil {
			return nil, fmt.Errorf("page size config: %w", err)
		}
		conf := model.NewDefaultConfiguration()
		return withWarnings(nil, warnings), api.ImportImagesFile(paths, outputPath, imp, conf)
	}

	if opts.OCR {
		return scanToPDFWithOCR(ctx, paths, outputPath, tesseractLang(opts.Language))
	}

	imp, err := importConfigForPageSize(opts.PageSize, opts.Margin)
	if err != nil {
		return nil, fmt.Errorf("page size config: %w", err)
	}
	conf := model.NewDefaultConfiguration()
	return nil, api.ImportImagesFile(paths, outputPath, imp, conf)
}

// scanToPDFWithOCR renders each image to a searchable PDF via tesseract and
// merges the results. Page size options do not apply on this path —
// tesseract emits pages matching the image dimensions. The returned metadata
// carries the extractedText sidecar (a job has one output file — the PDF —
// so text rides on job metadata; job-service scrubs it from LIST responses).
func scanToPDFWithOCR(ctx context.Context, inputPaths []string, outputPath string, lang string) (map[string]interface{}, error) {
	slog.Info("converting images to searchable PDF using OCR", "language", lang)

	if _, err := exec.LookPath("tesseract"); err != nil {
		return nil, fmt.Errorf("OCR requires tesseract to be installed")
	}

	tempDir, err := os.MkdirTemp("", "pdf-ocr-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	pdfFiles := make([]string, len(inputPaths))
	// Per-page .txt sidecars, page-order indexed; fallback and PDF passthrough
	// pages leave their slot empty so page breaks stay positionally aligned.
	txtFiles := make([]string, len(inputPaths))
	for i, imgPath := range inputPaths {
		// PDFs pass through OCR untouched (already document-form inputs).
		if strings.EqualFold(filepath.Ext(imgPath), ".pdf") {
			pdfFiles[i] = imgPath
			continue
		}

		baseName := filepath.Base(imgPath)
		nameWithoutExt := strings.TrimSuffix(baseName, filepath.Ext(baseName))
		outputBase := filepath.Join(tempDir, fmt.Sprintf("ocr_%d_%s", i, nameWithoutExt))

		cmd := exec.CommandContext(ctx, "tesseract", imgPath, outputBase, "-l", lang, "pdf", "txt")
		if err := cmd.Run(); err != nil {
			slog.Warn("OCR failed, falling back to non-OCR", "image", imgPath, "error", err)
			pdfFile := outputBase + ".pdf"
			if err := api.ImportImagesFile([]string{imgPath}, pdfFile, nil, nil); err != nil {
				return nil, fmt.Errorf("failed to process image %s: %w", imgPath, err)
			}
			pdfFiles[i] = pdfFile
		} else {
			pdfFiles[i] = outputBase + ".pdf"
			txtFiles[i] = outputBase + ".txt"
		}
	}

	metadata := map[string]interface{}{}
	if text, truncated := collectOCRText(txtFiles, ocrTextCapBytes); text != "" {
		metadata["extractedText"] = text
		if truncated {
			metadata["textTruncated"] = true
		}
	}

	if len(pdfFiles) == 1 {
		return metadata, copyFile(pdfFiles[0], outputPath)
	}
	return metadata, api.MergeCreateFile(pdfFiles, outputPath, false, nil)
}

// ocrTextCapBytes bounds the extracted-text sidecar stored in job metadata.
// 512 KB of UTF-8 is ~100+ dense pages — beyond it the text is truncated and
// flagged, never dropped.
const ocrTextCapBytes = 512 * 1024

// collectOCRText concatenates per-page tesseract .txt sidecars in page order,
// form-feed-separated so page boundaries survive. Empty/missing entries (OCR
// fallback or PDF-passthrough pages) contribute an empty page, keeping
// positions aligned with the merged PDF. Returns "" when no page produced text.
// NOTE: duplicated in optimize-pdf/processing/processing_free.go by design
// (§1 no-shared-code) — keep the two in sync.
func collectOCRText(txtPaths []string, capBytes int) (string, bool) {
	pages := make([]string, len(txtPaths))
	hasText := false
	for i, p := range txtPaths {
		if p == "" {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			slog.Warn("could not read OCR text sidecar", "path", p, "error", err)
			continue
		}
		pages[i] = strings.TrimRight(string(data), " \t\n\f")
		if pages[i] != "" {
			hasText = true
		}
	}
	if !hasText {
		return "", false
	}
	text := strings.Join(pages, "\f")
	if len(text) <= capBytes {
		return text, false
	}
	cut := capBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut], true
}

// withWarnings adds per-page warnings to job metadata (nil-safe, no-op when
// there are none). They ride on the completed job, like extractedText.
func withWarnings(meta map[string]interface{}, warnings []ScanWarning) map[string]interface{} {
	if len(warnings) == 0 {
		return meta
	}
	if meta == nil {
		meta = map[string]interface{}{}
	}
	meta["warnings"] = warnings
	return meta
}

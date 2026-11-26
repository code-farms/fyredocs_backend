package processing

import (
	"context"
	"log/slog"
	"strings"
)

// PDF → PowerPoint has two modes (options["mode"]):
//
//   - "editable" (default): LibreOffice's impress_pdf_import opens the PDF in
//     Impress — one slide per page, text as editable text boxes — and saves it
//     as .pptx. A PDF stores positioned glyphs rather than paragraphs, so slides
//     get roughly one text box per line and missing fonts are substituted.
//   - "image": one full-page image per slide (pdfToPptImages). Exact look, text
//     not editable.
//
// Editable falls back to image slides when the import fails, and up front for
// Indic scripts: impress_pdf_import scrambles Devanagari (matras land on the
// wrong letters — verified 2026-11-26 on a Hindi corpus PDF whose own text
// layer extracts correctly with pdftotext), and readable image slides beat
// garbled editable ones. Scans have no text layer and come out as image slides
// either way.
//
// The mode allowlist is duplicated in job-service/handlers/tool_options.go
// (pdfToPptModes) — by design, keep in sync.

const (
	pptModeEditable = "editable"
	pptModeImage    = "image"
)

// pptMode reads options["mode"]; anything other than "image" means editable.
func pptMode(options map[string]interface{}) string {
	if v, ok := options["mode"].(string); ok && strings.EqualFold(strings.TrimSpace(v), pptModeImage) {
		return pptModeImage
	}
	return pptModeEditable
}

// indicTextPages is how many leading pages are checked for Indic script.
const indicTextPages = 5

// hasIndicScript reports whether s contains a character from the Indic script
// blocks (Devanagari U+0900 through Sinhala U+0DFF: Bengali, Gurmukhi,
// Gujarati, Oriya, Tamil, Telugu, Kannada, Malayalam in between).
func hasIndicScript(s string) bool {
	for _, r := range s {
		if r >= 0x0900 && r <= 0x0DFF {
			return true
		}
	}
	return false
}

// pdfToPpt converts a PDF to .pptx in the requested mode.
func pdfToPpt(ctx context.Context, inputPath, outputPath string, options map[string]interface{}, onProgress ProgressFunc) error {
	if pptMode(options) == pptModeImage {
		return pdfToPptImages(ctx, inputPath, outputPath, onProgress)
	}
	if text, err := pdfText(ctx, inputPath, indicTextPages); err == nil && hasIndicScript(text) {
		slog.Info("pdf-to-ppt: Indic script detected, using image slides instead of editable", "input", inputPath)
		return pdfToPptImages(ctx, inputPath, outputPath, onProgress)
	}
	err := runWithProgressTicking(ctx, onProgress, func(ctx context.Context) error {
		return pdfToOfficeWithFilter(ctx, inputPath, outputPath, "pptx", "impress_pdf_import")
	})
	if err != nil {
		slog.Warn("editable PDF→PPTX failed, falling back to image slides", "input", inputPath, "error", err)
		return pdfToPptImages(ctx, inputPath, outputPath, onProgress)
	}
	return nil
}

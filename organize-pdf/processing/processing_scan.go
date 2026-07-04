package processing

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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

func (c *ScanCorners) complete() bool {
	return c != nil && c.TL != nil && c.TR != nil && c.BR != nil && c.BL != nil
}

func (c *ScanCorners) quad() imaging.Quad {
	return imaging.Quad{TL: *c.TL, TR: *c.TR, BR: *c.BR, BL: *c.BL}
}

// ScanPage carries per-page processing directives, index-aligned with the
// job's input files.
type ScanPage struct {
	Corners  *ScanCorners `json:"corners,omitempty"`
	Rotation int          `json:"rotation,omitempty"`
}

// ScanOptions is the full scan-to-pdf option payload.
type ScanOptions struct {
	OCR      bool       `json:"ocr"`
	Language string     `json:"language"`
	PageSize string     `json:"pageSize"` // "", auto, a4, letter, id
	Enhance  string     `json:"enhance"`  // "", none, grayscale, bw, color-boost
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
	case "", "auto", "a4", "letter", "id":
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
	for _, p := range opts.Pages {
		if p.Corners.complete() || p.Rotation != 0 {
			return true
		}
	}
	return false
}

// pageAspect returns the fixed output aspect (w/h) for a page size, or 0 to
// derive the aspect from the crop quad itself. Only the ID card imposes an
// aspect on the warp — A4/Letter are handled by pdfcpu page geometry.
func pageAspect(pageSize string) float64 {
	if pageSize == "id" {
		return idCardAspect
	}
	return 0
}

// processOnePage applies crop/warp, rotation, and enhancement to a decoded
// image. Returns the processed image and whether it is bilevel (bw).
func processOnePage(img image.Image, page *ScanPage, enhance string, aspect float64) (image.Image, bool, error) {
	out := img

	if page != nil && page.Corners.complete() {
		b := out.Bounds()
		quadNorm := page.Corners.quad().Clamp01()
		if err := quadNorm.Validate(); err != nil {
			slog.Warn("invalid crop quad; skipping warp", "error", err)
		} else {
			quadPx := quadNorm.Denormalize(b.Dx(), b.Dy())
			w, h := imaging.WarpOutputSize(quadPx, aspect)
			warped, err := imaging.WarpPerspective(out, quadPx, w, h)
			if err != nil {
				return nil, false, fmt.Errorf("perspective warp: %w", err)
			}
			out = warped
		}
	}

	if page != nil && page.Rotation != 0 {
		out = imaging.Rotate(out, page.Rotation)
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

	return out, isBilevel, nil
}

// preprocessScanImages runs the per-page pipeline over every input image,
// writing processed copies into scratchDir. PDF inputs pass through
// untouched (they are legal scan-to-pdf inputs). The returned slice is
// index-aligned with inputPaths.
func preprocessScanImages(ctx context.Context, inputPaths []string, opts ScanOptions, scratchDir string) ([]string, error) {
	processed := make([]string, len(inputPaths))
	for i, inputPath := range inputPaths {
		if err := ctx.Err(); err != nil {
			return nil, err
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
		pageNeedsWork := (opts.Enhance != "" && opts.Enhance != "none") ||
			(page != nil && (page.Corners.complete() || page.Rotation != 0))
		if !pageNeedsWork {
			processed[i] = inputPath
			continue
		}

		img, _, err := imaging.DecodeFile(inputPath)
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", filepath.Base(inputPath), err)
		}

		out, isBilevel, err := processOnePage(img, page, opts.Enhance, pageAspect(opts.PageSize))
		if err != nil {
			return nil, fmt.Errorf("process %s: %w", filepath.Base(inputPath), err)
		}

		base := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
		if isBilevel {
			outPath := filepath.Join(scratchDir, fmt.Sprintf("scan_%03d_%s.png", i, base))
			if err := imaging.EncodePNG(outPath, out); err != nil {
				return nil, err
			}
			processed[i] = outPath
		} else {
			outPath := filepath.Join(scratchDir, fmt.Sprintf("scan_%03d_%s.jpg", i, base))
			if err := imaging.EncodeJPEG(outPath, out, 92); err != nil {
				return nil, err
			}
			processed[i] = outPath
		}
	}
	return processed, nil
}

// importConfigForPageSize builds the pdfcpu import config for the requested
// output page size. auto/empty keeps the current behavior (page = image
// dimensions). Unknown values degrade to auto — job-service already rejected
// them for external callers.
func importConfigForPageSize(pageSize string) (*pdfcpu.Import, error) {
	switch strings.ToLower(strings.TrimSpace(pageSize)) {
	case "", "auto":
		return nil, nil
	case "a4":
		return api.Import("form:A4, pos:c, sc:1.0 rel", types.POINTS)
	case "letter":
		return api.Import("form:Letter, pos:c, sc:1.0 rel", types.POINTS)
	case "id":
		// ISO ID-1 card: 85.6 × 54 mm = 242.6 × 153.1 points.
		return api.Import("dim:243 153, pos:c, sc:1.0 rel", types.POINTS)
	default:
		return nil, nil
	}
}

func scanToPDF(ctx context.Context, inputPaths []string, outputPath string, options map[string]interface{}) error {
	slog.Info("converting images to PDF (scan-to-pdf)", "count", len(inputPaths))

	if len(inputPaths) == 0 {
		return fmt.Errorf("no input images provided")
	}

	opts := parseScanOptions(options)

	paths := inputPaths
	if needsPreprocessing(opts) {
		scratchDir, err := os.MkdirTemp("", "pdf-scan-*")
		if err != nil {
			return fmt.Errorf("failed to create scratch dir: %w", err)
		}
		defer os.RemoveAll(scratchDir)

		paths, err = preprocessScanImages(ctx, inputPaths, opts, scratchDir)
		if err != nil {
			return fmt.Errorf("scan preprocessing: %w", err)
		}

		if opts.OCR {
			// OCR must run inside this scope while scratch files still exist.
			return scanToPDFWithOCR(ctx, paths, outputPath, tesseractLang(opts.Language))
		}

		imp, err := importConfigForPageSize(opts.PageSize)
		if err != nil {
			return fmt.Errorf("page size config: %w", err)
		}
		conf := model.NewDefaultConfiguration()
		return api.ImportImagesFile(paths, outputPath, imp, conf)
	}

	if opts.OCR {
		return scanToPDFWithOCR(ctx, paths, outputPath, tesseractLang(opts.Language))
	}

	imp, err := importConfigForPageSize(opts.PageSize)
	if err != nil {
		return fmt.Errorf("page size config: %w", err)
	}
	conf := model.NewDefaultConfiguration()
	return api.ImportImagesFile(paths, outputPath, imp, conf)
}

// scanToPDFWithOCR renders each image to a searchable PDF via tesseract and
// merges the results. Page size options do not apply on this path —
// tesseract emits pages matching the image dimensions.
func scanToPDFWithOCR(ctx context.Context, inputPaths []string, outputPath string, lang string) error {
	slog.Info("converting images to searchable PDF using OCR", "language", lang)

	if _, err := exec.LookPath("tesseract"); err != nil {
		return fmt.Errorf("OCR requires tesseract to be installed")
	}

	tempDir, err := os.MkdirTemp("", "pdf-ocr-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	pdfFiles := make([]string, len(inputPaths))
	for i, imgPath := range inputPaths {
		// PDFs pass through OCR untouched (already document-form inputs).
		if strings.EqualFold(filepath.Ext(imgPath), ".pdf") {
			pdfFiles[i] = imgPath
			continue
		}

		baseName := filepath.Base(imgPath)
		nameWithoutExt := strings.TrimSuffix(baseName, filepath.Ext(baseName))
		outputBase := filepath.Join(tempDir, fmt.Sprintf("ocr_%d_%s", i, nameWithoutExt))

		cmd := exec.CommandContext(ctx, "tesseract", imgPath, outputBase, "-l", lang, "pdf")
		if err := cmd.Run(); err != nil {
			slog.Warn("OCR failed, falling back to non-OCR", "image", imgPath, "error", err)
			pdfFile := outputBase + ".pdf"
			if err := api.ImportImagesFile([]string{imgPath}, pdfFile, nil, nil); err != nil {
				return fmt.Errorf("failed to process image %s: %w", imgPath, err)
			}
			pdfFiles[i] = pdfFile
		} else {
			pdfFiles[i] = outputBase + ".pdf"
		}
	}

	if len(pdfFiles) == 1 {
		return copyFile(pdfFiles[0], outputPath)
	}
	return api.MergeCreateFile(pdfFiles, outputPath, false, nil)
}

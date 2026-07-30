package processing

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// ProgressFunc reports progress as a percentage (0-100).
type ProgressFunc func(percent int)

// pdfImageFormats maps the public `format` option to pdftoppm's flag and the
// output extension. A TRUST BOUNDARY like the OCR language allowlist: only
// mapped values ever reach the command line; anything else falls back to PNG.
// job-service validates the same set at the public boundary (tool_options.go)
// — this is defence in depth, duplicated by design (§1 no shared DTOs).
var pdfImageFormats = map[string]struct{ flag, ext string }{
	"":     {"-png", ".png"},
	"png":  {"-png", ".png"},
	"jpg":  {"-jpeg", ".jpg"},
	"jpeg": {"-jpeg", ".jpg"},
	"tiff": {"-tiff", ".tif"},
}

// Render DPI bounds for pdf-to-image; job-service rejects out-of-range values
// at the boundary, the worker re-clamps defensively.
const (
	pdfImageDefaultDPI = 150
	pdfImageMinDPI     = 72
	pdfImageMaxDPI     = 600
)

// pdfImageOptionString reads a string option, coercing JSON numbers so a
// client sending {"dpi": 300} works the same as {"dpi": "300"}.
func pdfImageOptionString(options map[string]interface{}, key string) string {
	v, ok := options[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return strings.Trim(string(b), `"`)
}

// parsePdfToImageOptions resolves {format, dpi} with safe defaults. Invalid
// values degrade rather than fail — job-service already rejected them for
// external callers.
func parsePdfToImageOptions(options map[string]interface{}) (flag string, ext string, dpi int) {
	format := strings.ToLower(strings.TrimSpace(pdfImageOptionString(options, "format")))
	spec, ok := pdfImageFormats[format]
	if !ok {
		spec = pdfImageFormats["png"]
	}

	dpi = pdfImageDefaultDPI
	if raw := pdfImageOptionString(options, "dpi"); raw != "" {
		if parsed, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
			dpi = parsed
		}
	}
	if dpi < pdfImageMinDPI {
		dpi = pdfImageMinDPI
	}
	if dpi > pdfImageMaxDPI {
		dpi = pdfImageMaxDPI
	}
	return spec.flag, spec.ext, dpi
}

// imageMaxDim bounds the longest edge of a rendered page in pixels so a large
// page at high DPI (especially uncompressed TIFF) cannot exhaust memory.
// NOTE: mirrors optimize-pdf's ocrMaxImageDim/computeSafeDPI pair (duplicated
// by design, §1 no-shared-code) — keep the clamping semantics in sync.
func imageMaxDim() int {
	const def = 10000
	if v := os.Getenv("IMAGE_MAX_IMAGE_DIM"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// computeSafeDPI returns a render resolution keeping the longest page edge
// within maxDim pixels, never exceeding requestedDPI (pts/72 * dpi = pixels).
// NOTE: duplicated from optimize-pdf/processing/processing_free.go by design.
func computeSafeDPI(widthPts, heightPts float64, requestedDPI, maxDim int) int {
	longest := widthPts
	if heightPts > longest {
		longest = heightPts
	}
	if longest <= 0 || maxDim <= 0 {
		return requestedDPI
	}

	fitDPI := int(float64(maxDim) * 72.0 / longest)
	if fitDPI >= requestedDPI {
		return requestedDPI
	}
	if fitDPI < 1 {
		return 1
	}
	return fitDPI
}

// pdfPageDimensions returns the first page's media-box size in PDF points via
// `pdfinfo` (poppler-utils). Used only to bound render DPI; errors non-fatal.
// NOTE: duplicated from optimize-pdf/processing/processing_free.go by design.
func pdfPageDimensions(ctx context.Context, inputPath string) (float64, float64, error) {
	out, err := exec.CommandContext(ctx, "pdfinfo", inputPath).Output()
	if err != nil {
		return 0, 0, fmt.Errorf("pdfinfo failed: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "Page size:") {
			continue
		}
		// Example: "Page size:      1224 x 1584 pts (letter)"
		rest := strings.TrimSpace(strings.TrimPrefix(line, "Page size:"))
		fields := strings.Fields(rest)
		if len(fields) < 3 || fields[1] != "x" {
			break
		}
		w, errW := strconv.ParseFloat(fields[0], 64)
		h, errH := strconv.ParseFloat(fields[2], 64)
		if errW != nil || errH != nil {
			break
		}
		return w, h, nil
	}
	return 0, 0, fmt.Errorf("could not parse page size from pdfinfo output")
}

// pdfToImages converts a PDF to images (PNG default; JPG/TIFF via the format
// option) at the requested DPI. Single-page PDFs return the bare image;
// multi-page PDFs return a ZIP. It returns the actual output path — the
// extension is the only channel job-service uses to name the download.
func pdfToImages(ctx context.Context, inputPath string, outputDir string, baseName string, options map[string]interface{}) (string, error) {
	formatFlag, ext, dpi := parsePdfToImageOptions(options)
	slog.Info("converting PDF to images", "input", inputPath, "format", formatFlag, "dpi", dpi)

	// Cap the effective DPI against page size so oversized renders can't
	// exhaust memory (uncompressed TIFF is the worst case).
	if w, h, err := pdfPageDimensions(ctx, inputPath); err == nil {
		if safe := computeSafeDPI(w, h, dpi, imageMaxDim()); safe != dpi {
			slog.Info("capping render DPI to bound image size", "requested", dpi, "using", safe)
			dpi = safe
		}
	}

	tempDir, err := os.MkdirTemp("", "pdf-images-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	pdfCtx, err := api.ReadContextFile(inputPath)
	if err != nil {
		return "", fmt.Errorf("failed to read PDF: %w", err)
	}

	// Convert all pages in a single pdftoppm invocation — spawning one process
	// per page dominated conversion time on large PDFs. pdftoppm zero-pads the
	// page index, so the globs below stay in page order.
	cmd := exec.CommandContext(ctx, "pdftoppm", formatFlag, "-r", strconv.Itoa(dpi), inputPath, filepath.Join(tempDir, "page"))
	if err := cmd.Run(); err != nil {
		slog.ErrorContext(ctx, "pdftoppm failed", "error", err, "op", "pdftoppm.render")
		return "", fmt.Errorf("PDF to image conversion requires pdftoppm (poppler-utils): %w", err)
	}

	// Single-page PDF: return the image directly instead of wrapping in ZIP.
	if pdfCtx.PageCount == 1 {
		imageFiles, _ := filepath.Glob(filepath.Join(tempDir, "*"+ext))
		if len(imageFiles) == 1 {
			outputPath := filepath.Join(outputDir, baseName+ext)
			if err := copyFile(imageFiles[0], outputPath); err != nil {
				return "", fmt.Errorf("failed to copy single page image: %w", err)
			}
			return outputPath, nil
		}
	}

	// Multi-page PDF: ZIP all page images.
	outputPath := filepath.Join(outputDir, baseName+".zip")
	return outputPath, zipDirectory(tempDir, outputPath)
}

// pdfToPdfa converts a PDF to the archival PDF/A profile via Ghostscript.
//
// Conversion can be lossy where the source uses features PDF/A forbids — notably
// transparency and non-embedded fonts, which get flattened or substituted. That is
// inherent to the format, not a defect here.
func pdfToPdfa(ctx context.Context, inputPath string, outputPath string) error {
	slog.Info("converting PDF to PDF/A", "input", inputPath)

	cmd := exec.CommandContext(ctx, "gs",
		"-dPDFA=2",
		"-dBATCH",
		"-dNOPAUSE",
		"-dNOOUTERSAVE",
		"-sProcessColorModel=DeviceRGB",
		"-sDEVICE=pdfwrite",
		"-sPDFACompatibilityPolicy=1",
		fmt.Sprintf("-sOutputFile=%s", outputPath),
		inputPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.ErrorContext(ctx, "ghostscript PDF/A conversion failed", "error", err, "op", "ghostscript.pdfa", "output", string(output))
		return fmt.Errorf("Ghostscript not available or conversion failed: %w", err)
	}

	slog.Info("PDF/A conversion completed", "output", outputPath)
	return nil
}

// pdfToOffice converts a PDF into a Word/Excel/PowerPoint format via LibreOffice.
//
// This direction is inherently approximate: a PDF describes positioned glyphs, not
// paragraphs or cells, so the structure LibreOffice reports is inferred. Expect
// layout fidelity rather than semantic fidelity.
func pdfToOffice(ctx context.Context, inputPath string, outputPath string, outputFormat string) error {
	slog.Info("converting PDF to office format", "format", outputFormat, "input", inputPath)

	outputDir := filepath.Dir(outputPath)

	cmd := exec.CommandContext(ctx, "libreoffice",
		"--headless",
		"--infilter=writer_pdf_import",
		"--convert-to", outputFormat,
		"--outdir", outputDir,
		inputPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.ErrorContext(ctx, "libreoffice conversion failed", "error", err, "op", "libreoffice.pdf_to_office", "format", outputFormat, "output", string(output))
		return fmt.Errorf("PDF to %s conversion failed: %w", outputFormat, err)
	}

	baseName := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	convertedFile := filepath.Join(outputDir, baseName+"."+outputFormat)

	// Verify LibreOffice actually produced the file BEFORE renaming. Some
	// conversions (e.g. importing a PDF as a Writer doc then exporting to a
	// spreadsheet format) silently produce nothing; renaming first would surface
	// a misleading "failed to rename" instead of the real cause.
	if info, statErr := os.Stat(convertedFile); statErr != nil || info.Size() == 0 {
		return fmt.Errorf("LibreOffice produced no output for %s conversion", outputFormat)
	}

	if convertedFile != outputPath {
		if err := os.Rename(convertedFile, outputPath); err != nil {
			return fmt.Errorf("failed to rename output file: %w", err)
		}
	}

	slog.Info("PDF to office conversion completed", "format", outputFormat, "output", outputPath)
	return nil
}

// pdfToPptImages converts a PDF to a PPTX where each page is an image slide.
// This is more reliable than the LibreOffice writer_pdf_import approach because
// it works with any PDF type (text, graphics, scanned docs).
func pdfToPptImages(ctx context.Context, inputPath string, outputPath string, onProgress ProgressFunc) error {
	slog.Info("converting PDF to PPTX (image-based)", "input", inputPath)

	tempDir, err := os.MkdirTemp("", "pdf-ppt-images-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	pdfCtx, err := api.ReadContextFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read PDF: %w", err)
	}
	pageCount := pdfCtx.PageCount
	if pageCount == 0 {
		return fmt.Errorf("PDF has no pages")
	}

	// Convert all pages in a single pdftoppm invocation (was one process per
	// page); synthetic ticking covers progress while the subprocess runs.
	// pdftoppm zero-pads the page index, so the builder's glob stays in order.
	if err := runWithProgressTicking(ctx, onProgress, func(ctx context.Context) error {
		return exec.CommandContext(ctx, "pdftoppm", "-png", inputPath, filepath.Join(tempDir, "page")).Run()
	}); err != nil {
		return fmt.Errorf("pdftoppm failed: %w", err)
	}

	// Build the PPTX from the images.
	if onProgress != nil {
		onProgress(85)
	}
	if err := buildPptxFromImages(tempDir, outputPath); err != nil {
		return fmt.Errorf("failed to build PPTX: %w", err)
	}

	slog.Info("PDF to PPTX conversion completed", "pages", pageCount, "output", outputPath)
	return nil
}

// pdfToOfficeTicking wraps pdfToOffice with synthetic progress ticking for
// conversion tools that call a single long-running subprocess (odt, etc.).
func pdfToOfficeTicking(ctx context.Context, inputPath string, outputPath string, outputFormat string, onProgress ProgressFunc) error {
	return runWithProgressTicking(ctx, onProgress, func(ctx context.Context) error {
		return pdfToOffice(ctx, inputPath, outputPath, outputFormat)
	})
}

// pdfToXlsxScript is the path to the pdfplumber/openpyxl helper (see Dockerfile).
func pdfToXlsxScript() string {
	if p := os.Getenv("PDF_TO_XLSX_SCRIPT"); p != "" {
		return p
	}
	return "/app/scripts/pdf_to_xlsx.py"
}

// pdfToXlsx converts a PDF to XLSX via pdfplumber table extraction (with a text
// fallback for non-tabular pages). LibreOffice's writer_pdf_import cannot export
// a spreadsheet, so a real extractor is used instead.
func pdfToXlsx(ctx context.Context, inputPath string, outputPath string) error {
	slog.Info("converting PDF to XLSX via pdfplumber", "input", inputPath)
	cmd := exec.CommandContext(ctx, "python3", pdfToXlsxScript(), inputPath, outputPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.ErrorContext(ctx, "pdf_to_xlsx failed", "error", err, "op", "pdf_to_xlsx", "output", string(output))
		return fmt.Errorf("PDF to XLSX conversion failed: %w", err)
	}
	if info, statErr := os.Stat(outputPath); statErr != nil || info.Size() == 0 {
		return fmt.Errorf("PDF to XLSX conversion produced no output")
	}
	return nil
}

// pdfToXlsxTicking runs the xlsx conversion while reporting progress.
//
// LibreOffice gives no progress output, so the "ticking" variants advance a
// synthetic progress estimate on a timer. It exists so a long conversion does not
// look hung in the UI; the numbers are an estimate, not a measurement.
func pdfToXlsxTicking(ctx context.Context, inputPath string, outputPath string, onProgress ProgressFunc) error {
	return runWithProgressTicking(ctx, onProgress, func(ctx context.Context) error {
		return pdfToXlsx(ctx, inputPath, outputPath)
	})
}

// pdfToOds builds an XLSX first (real extraction) then converts it to ODS with
// LibreOffice — a spreadsheet→spreadsheet conversion Calc supports (unlike the
// broken PDF→ODS-via-Writer path).
func pdfToOds(ctx context.Context, inputPath string, outputPath string) error {
	outputDir := filepath.Dir(outputPath)
	tmpXlsx := filepath.Join(outputDir, "pdf2ods_"+strings.TrimSuffix(filepath.Base(outputPath), filepath.Ext(outputPath))+".xlsx")
	if err := pdfToXlsx(ctx, inputPath, tmpXlsx); err != nil {
		return err
	}
	defer os.Remove(tmpXlsx)
	return officeConvertTo(ctx, tmpXlsx, outputPath, "ods")
}

// pdfToOdsTicking is pdfToXlsxTicking for the OpenDocument spreadsheet target.
// Same synthetic-progress caveat.
func pdfToOdsTicking(ctx context.Context, inputPath string, outputPath string, onProgress ProgressFunc) error {
	return runWithProgressTicking(ctx, onProgress, func(ctx context.Context) error {
		return pdfToOds(ctx, inputPath, outputPath)
	})
}

// pdfToOdp builds the image-slide PPTX (works for any PDF) then converts it to
// ODP with LibreOffice — an Impress→Impress conversion (unlike the broken
// PDF→ODP-via-Writer path).
func pdfToOdp(ctx context.Context, inputPath string, outputPath string, onProgress ProgressFunc) error {
	outputDir := filepath.Dir(outputPath)
	tmpPptx := filepath.Join(outputDir, "pdf2odp_"+strings.TrimSuffix(filepath.Base(outputPath), filepath.Ext(outputPath))+".pptx")
	if err := pdfToPptImages(ctx, inputPath, tmpPptx, onProgress); err != nil {
		return err
	}
	defer os.Remove(tmpPptx)
	return officeConvertTo(ctx, tmpPptx, outputPath, "odp")
}

// officeConvertTo runs a plain LibreOffice --convert-to for same-family
// conversions (e.g. xlsx→ods, pptx→odp), then renames to the requested output.
func officeConvertTo(ctx context.Context, inputPath string, outputPath string, outputFormat string) error {
	outputDir := filepath.Dir(outputPath)
	cmd := exec.CommandContext(ctx, "libreoffice", "--headless",
		"--convert-to", outputFormat, "--outdir", outputDir, inputPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		slog.ErrorContext(ctx, "libreoffice convert failed", "error", err, "op", "libreoffice.convert", "format", outputFormat, "output", string(output))
		return fmt.Errorf("conversion to %s failed: %w", outputFormat, err)
	}
	baseName := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	convertedFile := filepath.Join(outputDir, baseName+"."+outputFormat)
	if info, statErr := os.Stat(convertedFile); statErr != nil || info.Size() == 0 {
		return fmt.Errorf("LibreOffice produced no output for %s conversion", outputFormat)
	}
	if convertedFile != outputPath {
		if err := os.Rename(convertedFile, outputPath); err != nil {
			return fmt.Errorf("failed to rename output file: %w", err)
		}
	}
	return nil
}

// runWithProgressTicking runs convert in a goroutine while emitting synthetic
// progress ticks every 10s (30 → 80%, capped). On completion it bumps to 85%.
// On context cancellation it waits for the worker so the underlying
// CommandContext can finish cleaning up. Used by every PDF→office path that
// blocks on a single long-running subprocess (LibreOffice, pdf2docx, ...).
func runWithProgressTicking(ctx context.Context, onProgress ProgressFunc, convert func(context.Context) error) error {
	if onProgress != nil {
		onProgress(30)
	}

	done := make(chan error, 1)
	go func() {
		done <- convert(ctx)
	}()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	progress := 30
	for {
		select {
		case err := <-done:
			if err == nil && onProgress != nil {
				onProgress(85)
			}
			return err
		case <-ticker.C:
			if progress < 80 && onProgress != nil {
				progress += 5
				onProgress(progress)
			}
		case <-ctx.Done():
			return <-done
		}
	}
}

// pdfToDocxNative converts PDF to DOCX using the pdf2docx Python tool, which
// reconstructs real paragraphs/lists/tables from the PDF instead of dumping
// page contents as floating text frames the way LibreOffice's
// writer_pdf_import filter does. Falls back to LibreOffice when the binary is
// missing or fails — see processing.go dispatch.
func pdfToDocxNative(ctx context.Context, inputPath string, outputPath string) error {
	slog.Info("converting PDF to DOCX via pdf2docx", "input", inputPath)

	cmd := exec.CommandContext(ctx, "pdf2docx", "convert", inputPath, outputPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// CombinedOutput already includes stderr; surface it for debugging
		// without exposing the raw subprocess to upstream callers.
		return fmt.Errorf("pdf2docx convert failed: %w (output: %s)", err, strings.TrimSpace(string(output)))
	}

	info, statErr := os.Stat(outputPath)
	if statErr != nil || info.Size() == 0 {
		return fmt.Errorf("pdf2docx produced no output for %s", inputPath)
	}

	slog.Info("pdf2docx conversion completed", "output", outputPath, "bytes", info.Size())
	return nil
}

// pdfToDocxNativeTicking wraps pdfToDocxNative with the same progress ticker
// pdfToOfficeTicking uses.
func pdfToDocxNativeTicking(ctx context.Context, inputPath string, outputPath string, onProgress ProgressFunc) error {
	return runWithProgressTicking(ctx, onProgress, func(ctx context.Context) error {
		return pdfToDocxNative(ctx, inputPath, outputPath)
	})
}

// pdfToHTML converts a PDF to HTML plus its extracted assets, returned as a ZIP
// since the conversion produces multiple files and a job has one output.
func pdfToHTML(ctx context.Context, inputPath string, outputPath string) error {
	slog.Info("converting PDF to HTML", "input", inputPath)

	tempDir, err := os.MkdirTemp("", "pdf-html-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	outputBase := filepath.Join(tempDir, "output")
	cmd := exec.CommandContext(ctx, "pdftohtml", "-s", "-noframes", inputPath, outputBase)

	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.ErrorContext(ctx, "pdftohtml failed", "error", err, "op", "pdftohtml.convert", "output", string(output))
		return fmt.Errorf("PDF to HTML conversion failed: %w", err)
	}

	return zipDirectory(tempDir, outputPath)
}

// pdfToText extracts the text layer. Returns empty or garbled output for a
// scanned PDF that has no text layer — OCR (optimize-pdf) is the tool for that
// case, and this one cannot detect the difference.
func pdfToText(ctx context.Context, inputPath string, outputPath string) error {
	slog.Info("converting PDF to text", "input", inputPath)

	cmd := exec.CommandContext(ctx, "pdftotext", "-layout", inputPath, outputPath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.ErrorContext(ctx, "pdftotext failed", "error", err, "op", "pdftotext.convert", "output", string(output))
		return fmt.Errorf("PDF to text conversion failed: %w", err)
	}

	slog.Info("PDF to text conversion completed", "output", outputPath)
	return nil
}

// zipDirectory archives every file under sourceDir with paths relative to it, so
// the archive contains no absolute paths. Used where a conversion emits multiple
// files but a job has exactly one output object.
func zipDirectory(sourceDir string, zipPath string) error {
	zipFile, err := os.Create(zipPath)
	if err != nil {
		return fmt.Errorf("failed to create zip: %w", err)
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)

	if err := filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}

		zipEntry, err := zipWriter.Create(relPath)
		if err != nil {
			return err
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(zipEntry, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}); err != nil {
		zipWriter.Close()
		return err
	}

	if err := zipWriter.Close(); err != nil {
		return fmt.Errorf("failed to finalize zip: %w", err)
	}
	return nil
}

// copyFile copies src to dst, surfacing a deferred Close error so a failed flush
// is not mistaken for success.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		if e := out.Close(); e != nil && err == nil {
			err = e
		}
	}()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

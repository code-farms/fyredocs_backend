package processing

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// ProgressFunc reports progress as a percentage (0-100).
type ProgressFunc func(percent int)

// pdfToImages converts a PDF to PNG images. For single-page PDFs the output
// is a single PNG file; for multi-page PDFs the output is a ZIP of PNGs.
// It returns the actual output path (which may end in .png or .zip).
func pdfToImages(ctx context.Context, inputPath string, outputDir string, baseName string) (string, error) {
	slog.Info("converting PDF to images", "input", inputPath)

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
	cmd := exec.CommandContext(ctx, "pdftoppm", "-png", inputPath, filepath.Join(tempDir, "page"))
	if err := cmd.Run(); err != nil {
		slog.ErrorContext(ctx, "pdftoppm failed", "error", err, "op", "pdftoppm.render")
		return "", fmt.Errorf("PDF to image conversion requires pdftoppm (poppler-utils): %w", err)
	}

	// Single-page PDF: return the PNG directly instead of wrapping in ZIP.
	if pdfCtx.PageCount == 1 {
		pngFiles, _ := filepath.Glob(filepath.Join(tempDir, "*.png"))
		if len(pngFiles) == 1 {
			outputPath := filepath.Join(outputDir, baseName+".png")
			if err := copyFile(pngFiles[0], outputPath); err != nil {
				return "", fmt.Errorf("failed to copy single page image: %w", err)
			}
			return outputPath, nil
		}
	}

	// Multi-page PDF: ZIP all PNGs.
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

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
		slog.Error("pdftoppm failed", "error", err)
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
		slog.Error("ghostscript PDF/A conversion failed", "output", string(output))
		return fmt.Errorf("Ghostscript not available or conversion failed: %w", err)
	}

	slog.Info("PDF/A conversion completed", "output", outputPath)
	return nil
}

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
		slog.Error("libreoffice conversion failed", "output", string(output))
		return fmt.Errorf("PDF to %s conversion failed: %w", outputFormat, err)
	}

	baseName := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	convertedFile := filepath.Join(outputDir, baseName+"."+outputFormat)

	if convertedFile != outputPath {
		if err := os.Rename(convertedFile, outputPath); err != nil {
			return fmt.Errorf("failed to rename output file: %w", err)
		}
	}

	// Validate that LibreOffice actually produced output.
	info, statErr := os.Stat(outputPath)
	if statErr != nil || info.Size() == 0 {
		return fmt.Errorf("LibreOffice produced no output for %s conversion", outputFormat)
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
// conversion tools that call a single long-running subprocess (xlsx, odt, etc.).
func pdfToOfficeTicking(ctx context.Context, inputPath string, outputPath string, outputFormat string, onProgress ProgressFunc) error {
	return runWithProgressTicking(ctx, onProgress, func(ctx context.Context) error {
		return pdfToOffice(ctx, inputPath, outputPath, outputFormat)
	})
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
		slog.Error("pdftohtml failed", "output", string(output))
		return fmt.Errorf("PDF to HTML conversion failed: %w", err)
	}

	return zipDirectory(tempDir, outputPath)
}

func pdfToText(ctx context.Context, inputPath string, outputPath string) error {
	slog.Info("converting PDF to text", "input", inputPath)

	cmd := exec.CommandContext(ctx, "pdftotext", "-layout", inputPath, outputPath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("pdftotext failed", "output", string(output))
		return fmt.Errorf("PDF to text conversion failed: %w", err)
	}

	slog.Info("PDF to text conversion completed", "output", outputPath)
	return nil
}

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

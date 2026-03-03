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

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

func pdfToImages(ctx context.Context, inputPath string, outputPath string) error {
	slog.Info("converting PDF to images", "input", inputPath)

	tempDir, err := os.MkdirTemp("", "pdf-images-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	pdfCtx, err := api.ReadContextFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read PDF: %w", err)
	}

	for i := 1; i <= pdfCtx.PageCount; i++ {
		cmd := exec.CommandContext(ctx, "pdftoppm", "-png", "-f", fmt.Sprintf("%d", i), "-l", fmt.Sprintf("%d", i), inputPath, filepath.Join(tempDir, fmt.Sprintf("page_%03d", i)))
		if err := cmd.Run(); err != nil {
			slog.Error("pdftoppm failed", "page", i, "error", err)
			return fmt.Errorf("PDF to image conversion requires pdftoppm (poppler-utils): %w", err)
		}
	}

	return zipDirectory(tempDir, outputPath)
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

	slog.Info("PDF to office conversion completed", "format", outputFormat, "output", outputPath)
	return nil
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
	defer zipWriter.Close()

	return filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
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
	})
}

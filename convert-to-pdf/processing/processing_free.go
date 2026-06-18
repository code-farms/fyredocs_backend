package processing

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// tryUnoconvert attempts a conversion through a pooled unoserver daemon. It
// blocks until a daemon port is free (bounding concurrency to the pool size),
// runs unoconvert against it, then releases the port BEFORE returning so the
// slow LibreOffice fallback never holds a daemon slot. Returns true on success;
// false (output already logged) means the caller should fall back.
func tryUnoconvert(ctx context.Context, inputPath, outputPath, convertTo string) bool {
	port, ok := pool.acquire(ctx)
	if !ok {
		return false
	}
	defer pool.release(port)

	// Use a 30s timeout so a hung daemon falls back faster.
	unoCtx, unoCancel := context.WithTimeout(ctx, 30*time.Second)
	defer unoCancel()
	cmd := exec.CommandContext(unoCtx, "unoconvert",
		"--host", pool.host, "--port", port,
		"--convert-to", convertTo,
		inputPath, outputPath)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return true
	}
	slog.Warn("unoconvert failed, falling back to direct libreoffice",
		"port", port, "convertTo", convertTo, "output", string(output), "error", err)
	return false
}

func officeToPDF(ctx context.Context, inputPath string, outputPath string, fileType string) error {
	slog.Info("converting to PDF", "type", fileType, "input", inputPath)

	// Fast path: unoconvert via a pooled persistent LibreOffice daemon.
	if tryUnoconvert(ctx, inputPath, outputPath, "pdf") {
		return nil
	}

	// Slow fallback: spawn a fresh LibreOffice process.
	outputDir := filepath.Dir(outputPath)
	cmd := exec.CommandContext(ctx, "libreoffice", "--headless", "--convert-to", "pdf", "--outdir", outputDir, inputPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("libreoffice fallback failed", "output", string(output))
		return fmt.Errorf("conversion failed: %w", err)
	}

	convertedFile := filepath.Join(outputDir, strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))+".pdf")
	if convertedFile != outputPath {
		return os.Rename(convertedFile, outputPath)
	}
	return nil
}

func officeToOffice(ctx context.Context, inputPath string, outputPath string, outputFormat string) error {
	slog.Info("converting office to ODF", "format", outputFormat, "input", inputPath)

	// Fast path: unoconvert via a pooled persistent LibreOffice daemon.
	if tryUnoconvert(ctx, inputPath, outputPath, outputFormat) {
		return nil
	}

	// Slow fallback: spawn a fresh LibreOffice process.
	outputDir := filepath.Dir(outputPath)
	cmd := exec.CommandContext(ctx, "libreoffice", "--headless", "--convert-to", outputFormat, "--outdir", outputDir, inputPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("libreoffice fallback failed", "output", string(output))
		return fmt.Errorf("conversion to %s failed: %w", outputFormat, err)
	}

	convertedFile := filepath.Join(outputDir, strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))+"."+outputFormat)
	if convertedFile != outputPath {
		return os.Rename(convertedFile, outputPath)
	}
	return nil
}

func imageToPDF(inputPaths []string, outputPath string) error {
	slog.Info("converting images to PDF", "count", len(inputPaths))
	return api.ImportImagesFile(inputPaths, outputPath, nil, nil)
}

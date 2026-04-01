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

// unoserver connection settings (configurable via environment variables).
var (
	unoHost = envOrDefault("UNOSERVER_HOST", "127.0.0.1")
	unoPort = envOrDefault("UNOSERVER_PORT", "2002")
)

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func officeToPDF(ctx context.Context, inputPath string, outputPath string, fileType string) error {
	slog.Info("converting to PDF", "type", fileType, "input", inputPath)

	// Fast path: unoconvert via persistent LibreOffice daemon.
	// Use a 30s timeout so hung unoconvert falls back faster.
	unoCtx, unoCancel := context.WithTimeout(ctx, 30*time.Second)
	defer unoCancel()
	cmd := exec.CommandContext(unoCtx, "unoconvert",
		"--host", unoHost, "--port", unoPort,
		"--convert-to", "pdf",
		inputPath, outputPath)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	slog.Warn("unoconvert failed, falling back to direct libreoffice",
		"output", string(output), "error", err)

	// Slow fallback: spawn a fresh LibreOffice process.
	outputDir := filepath.Dir(outputPath)
	cmd = exec.CommandContext(ctx, "libreoffice", "--headless", "--convert-to", "pdf", "--outdir", outputDir, inputPath)
	output, err = cmd.CombinedOutput()
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

	// Fast path: unoconvert via persistent LibreOffice daemon.
	unoCtx, unoCancel := context.WithTimeout(ctx, 30*time.Second)
	defer unoCancel()
	cmd := exec.CommandContext(unoCtx, "unoconvert",
		"--host", unoHost, "--port", unoPort,
		"--convert-to", outputFormat,
		inputPath, outputPath)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	slog.Warn("unoconvert failed, falling back to direct libreoffice",
		"output", string(output), "error", err)

	// Slow fallback: spawn a fresh LibreOffice process.
	outputDir := filepath.Dir(outputPath)
	cmd = exec.CommandContext(ctx, "libreoffice", "--headless", "--convert-to", outputFormat, "--outdir", outputDir, inputPath)
	output, err = cmd.CombinedOutput()
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

package processing

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"golang.org/x/sync/errgroup"
)

func compressPDF(ctx context.Context, inputPath string, outputPath string, options map[string]interface{}) (map[string]interface{}, error) {
	slog.Info("compressing PDF", "input", inputPath)

	inputInfo, err := os.Stat(inputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat input file: %w", err)
	}
	originalSize := inputInfo.Size()

	quality, _ := optionString(options, "quality")

	gsPath, err := findGhostscript()
	if err != nil {
		return nil, err
	}

	// Map frontend quality levels to Ghostscript compression settings
	pdfSettings := "/ebook"
	dpi := "150"
	switch quality {
	case "low":
		pdfSettings = "/printer"
		dpi = "300"
	case "medium":
		pdfSettings = "/ebook"
		dpi = "150"
	case "high":
		pdfSettings = "/screen"
		dpi = "72"
	case "extreme":
		pdfSettings = "/screen"
		dpi = "36"
	}

	args := []string{
		"-dNOPAUSE",
		"-dBATCH",
		"-dSAFER",
		"-sDEVICE=pdfwrite",
		"-dCompatibilityLevel=1.4",
		"-dPDFSETTINGS=" + pdfSettings,
		"-dDetectDuplicateImages=true",
		"-dCompressFonts=true",
		"-dDownsampleColorImages=true",
		"-dColorImageResolution=" + dpi,
		"-dDownsampleGrayImages=true",
		"-dGrayImageResolution=" + dpi,
		"-dDownsampleMonoImages=true",
		"-dMonoImageResolution=" + dpi,
		"-sOutputFile=" + outputPath,
		inputPath,
	}

	cmd := exec.CommandContext(ctx, gsPath, args...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ghostscript compression failed: %w", err)
	}

	outputInfo, err := os.Stat(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat output file: %w", err)
	}
	compressedSize := outputInfo.Size()

	var compressionRatio float64
	if originalSize > 0 {
		compressionRatio = float64(originalSize-compressedSize) / float64(originalSize) * 100
	}

	metadata := map[string]interface{}{
		"originalSizeBytes":   originalSize,
		"compressedSizeBytes": compressedSize,
		"compressionRatio":    fmt.Sprintf("%.2f%%", compressionRatio),
		"quality":             quality,
	}

	slog.Info("compression complete", "originalBytes", originalSize, "compressedBytes", compressedSize, "reduction", fmt.Sprintf("%.2f%%", compressionRatio))

	return metadata, nil
}

func repairPDF(ctx context.Context, inputPath string, outputPath string) error {
	slog.Info("repairing PDF using Ghostscript", "input", inputPath)

	gsPath, err := findGhostscript()
	if err != nil {
		return err
	}

	args := []string{
		"-dNOPAUSE",
		"-dBATCH",
		"-dSAFER",
		"-sDEVICE=pdfwrite",
		"-dCompatibilityLevel=1.4",
		"-dPDFSETTINGS=/prepress",
		"-dDetectDuplicateImages=true",
		"-dCompressFonts=true",
		fmt.Sprintf("-sOutputFile=%s", outputPath),
		inputPath,
	}

	cmd := exec.CommandContext(ctx, gsPath, args...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ghostscript repair failed: %w", err)
	}

	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return fmt.Errorf("repair failed: output file not created")
	}

	slog.Info("PDF repair complete", "output", outputPath)
	return nil
}

func findGhostscript() (string, error) {
	candidates := []string{"gs", "ghostscript", "gswin64c", "gswin32c"}
	for _, name := range candidates {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("ghostscript not found. Install with: apk add ghostscript")
}

func ocrPDF(ctx context.Context, inputPath string, outputPath string, options map[string]interface{}) (map[string]interface{}, error) {
	slog.Info("adding OCR layer to PDF", "input", inputPath)

	if _, err := exec.LookPath("pdftoppm"); err != nil {
		return nil, fmt.Errorf("pdftoppm not found. Install with: apk add poppler-utils")
	}
	if _, err := exec.LookPath("tesseract"); err != nil {
		return nil, fmt.Errorf("tesseract not found. Install with: apk add tesseract-ocr")
	}

	// Map ISO 639-1 (frontend) to ISO 639-2/T (tesseract) language codes.
	var langMap = map[string]string{
		"en": "eng", "es": "spa", "fr": "fra", "de": "deu",
		"it": "ita", "pt": "por", "zh": "chi_sim", "ja": "jpn",
		"ko": "kor", "ar": "ara",
	}

	language, _ := optionString(options, "language")
	if language == "" {
		language = os.Getenv("OCR_DEFAULT_LANGUAGE")
		if language == "" {
			language = "eng"
		}
	}
	if mapped, ok := langMap[language]; ok {
		language = mapped
	}

	dpi := 150
	if envDpi := os.Getenv("OCR_DEFAULT_DPI"); envDpi != "" {
		if parsed, err := strconv.Atoi(envDpi); err == nil && parsed > 0 {
			dpi = parsed
		}
	}
	if dpiStr, _ := optionString(options, "dpi"); dpiStr != "" {
		if parsed, err := strconv.Atoi(dpiStr); err == nil && parsed > 0 {
			dpi = parsed
		}
	}

	tempDir, err := os.MkdirTemp("", "pdf-ocr-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	slog.Info("converting PDF to images for OCR", "dpi", dpi, "input", inputPath)

	imagePrefix := filepath.Join(tempDir, "page")
	cmd := exec.CommandContext(ctx, "pdftoppm",
		"-png",
		"-r", strconv.Itoa(dpi),
		inputPath,
		imagePrefix,
	)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pdftoppm conversion failed: %w", err)
	}

	imageFiles, err := filepath.Glob(filepath.Join(tempDir, "page-*.png"))
	if err != nil || len(imageFiles) == 0 {
		imageFiles, _ = filepath.Glob(filepath.Join(tempDir, "page*.png"))
	}
	if len(imageFiles) == 0 {
		return nil, fmt.Errorf("no images generated from PDF")
	}

	sort.Strings(imageFiles)
	slog.Info("PDF to image conversion complete", "imageCount", len(imageFiles))

	// Process pages in parallel using a worker pool sized to available CPUs
	// (capped at 4 to avoid excessive memory usage from concurrent tesseract).
	workers := runtime.NumCPU()
	if workers > 4 {
		workers = 4
	}
	if workers < 1 {
		workers = 1
	}

	pdfFiles := make([]string, len(imageFiles))
	var mu sync.Mutex // guards slog calls for cleaner output
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(workers)

	for i, imgPath := range imageFiles {
		i, imgPath := i, imgPath
		g.Go(func() error {
			mu.Lock()
			slog.Info("processing OCR page", "page", i+1, "total", len(imageFiles))
			mu.Unlock()

			baseName := filepath.Base(imgPath)
			nameWithoutExt := strings.TrimSuffix(baseName, filepath.Ext(baseName))
			outputBase := filepath.Join(tempDir, fmt.Sprintf("ocr_%d_%s", i, nameWithoutExt))

			cmd := exec.CommandContext(gctx, "tesseract",
				imgPath,
				outputBase,
				"-l", language,
				"pdf",
			)
			if err := cmd.Run(); err != nil {
				slog.Warn("OCR failed for page, falling back", "page", i+1, "error", err)
				pdfFile := outputBase + ".pdf"
				if err := api.ImportImagesFile([]string{imgPath}, pdfFile, nil, nil); err != nil {
					return fmt.Errorf("failed to process page %d: %w", i+1, err)
				}
				pdfFiles[i] = pdfFile
			} else {
				pdfFiles[i] = outputBase + ".pdf"
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	if len(pdfFiles) == 1 {
		if err := copyFile(pdfFiles[0], outputPath); err != nil {
			return nil, err
		}
	} else {
		if err := api.MergeCreateFile(pdfFiles, outputPath, false, nil); err != nil {
			return nil, fmt.Errorf("failed to merge OCR pages: %w", err)
		}
	}

	metadata := map[string]interface{}{
		"language":   language,
		"dpi":        dpi,
		"pagesOCRed": len(imageFiles),
	}

	slog.Info("OCR complete", "pagesProcessed", len(imageFiles), "language", language)
	return metadata, nil
}

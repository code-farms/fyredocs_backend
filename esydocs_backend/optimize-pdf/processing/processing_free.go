package processing

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// ==================== COMPRESS PDF ====================

// compressPDF optimizes/compresses a PDF file using pdfcpu
// Options:
//   - quality: "screen" (72dpi), "ebook" (150dpi), "printer" (300dpi), "prepress" (300dpi+)
func compressPDF(inputPath string, outputPath string, options map[string]interface{}) (map[string]interface{}, error) {
	log.Printf("[INFO] Compressing PDF %s", inputPath)

	// Get original file size
	inputInfo, err := os.Stat(inputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat input file: %w", err)
	}
	originalSize := inputInfo.Size()

	// Use pdfcpu optimize
	conf := model.NewDefaultConfiguration()

	// Apply optimization settings based on quality option
	quality, _ := optionString(options, "quality")
	switch quality {
	case "screen":
		// Maximum compression, lower quality
		conf.OptimizeDuplicateContentStreams = true
	case "ebook":
		// Balanced compression
		conf.OptimizeDuplicateContentStreams = true
	case "printer", "prepress":
		// Minimal compression, preserve quality
		conf.OptimizeDuplicateContentStreams = false
	default:
		// Default: balanced
		conf.OptimizeDuplicateContentStreams = true
	}

	if err := api.OptimizeFile(inputPath, outputPath, conf); err != nil {
		return nil, fmt.Errorf("pdfcpu optimization failed: %w", err)
	}

	// Get compressed file size
	outputInfo, err := os.Stat(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat output file: %w", err)
	}
	compressedSize := outputInfo.Size()

	// Calculate compression ratio
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

	log.Printf("[INFO] Compression complete: %d bytes -> %d bytes (%.2f%% reduction)",
		originalSize, compressedSize, compressionRatio)

	return metadata, nil
}

// ==================== REPAIR PDF ====================

// repairPDF uses Ghostscript to rebuild a potentially corrupted PDF
func repairPDF(inputPath string, outputPath string) error {
	log.Printf("[INFO] Repairing PDF %s using Ghostscript", inputPath)

	// Check if Ghostscript is available
	gsPath, err := findGhostscript()
	if err != nil {
		return err
	}

	// Ghostscript command to repair/rebuild PDF
	// -dPDFSETTINGS=/prepress preserves quality while rebuilding
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

	cmd := exec.Command(gsPath, args...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ghostscript repair failed: %w", err)
	}

	// Verify output was created
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return fmt.Errorf("repair failed: output file not created")
	}

	log.Printf("[INFO] PDF repair complete: %s", outputPath)
	return nil
}

// findGhostscript locates the Ghostscript executable
func findGhostscript() (string, error) {
	// Try common Ghostscript executable names
	candidates := []string{"gs", "ghostscript", "gswin64c", "gswin32c"}
	for _, name := range candidates {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("ghostscript not found. Install with: apk add ghostscript")
}

// ==================== OCR PDF ====================

// ocrPDF adds a searchable text layer to a scanned PDF
// Process: PDF -> Images (pdftoppm) -> OCR (tesseract) -> Merge back to PDF
// Options:
//   - language: OCR language code (default "eng")
//   - dpi: Resolution for conversion (default 300)
func ocrPDF(inputPath string, outputPath string, options map[string]interface{}) (map[string]interface{}, error) {
	log.Printf("[INFO] Adding OCR layer to PDF %s", inputPath)

	// Check dependencies
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		return nil, fmt.Errorf("pdftoppm not found. Install with: apk add poppler-utils")
	}
	if _, err := exec.LookPath("tesseract"); err != nil {
		return nil, fmt.Errorf("tesseract not found. Install with: apk add tesseract-ocr")
	}

	// Parse options
	language, _ := optionString(options, "language")
	if language == "" {
		language = "eng"
	}
	dpiStr, _ := optionString(options, "dpi")
	dpi := 300
	if dpiStr != "" {
		if parsed, err := strconv.Atoi(dpiStr); err == nil && parsed > 0 {
			dpi = parsed
		}
	}

	// Create temp directory for intermediate files
	tempDir, err := os.MkdirTemp("", "pdf-ocr-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Step 1: Convert PDF pages to images using pdftoppm
	imagePrefix := filepath.Join(tempDir, "page")
	cmd := exec.Command("pdftoppm",
		"-png",
		"-r", strconv.Itoa(dpi),
		inputPath,
		imagePrefix,
	)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pdftoppm conversion failed: %w", err)
	}

	// Step 2: Find all generated images
	imageFiles, err := filepath.Glob(filepath.Join(tempDir, "page-*.png"))
	if err != nil || len(imageFiles) == 0 {
		// Try alternative pattern (single page PDFs)
		imageFiles, _ = filepath.Glob(filepath.Join(tempDir, "page*.png"))
	}
	if len(imageFiles) == 0 {
		return nil, fmt.Errorf("no images generated from PDF")
	}

	// Sort images by page number
	sort.Strings(imageFiles)

	// Step 3: Run OCR on each image and create searchable PDFs
	var pdfFiles []string
	for i, imgPath := range imageFiles {
		baseName := filepath.Base(imgPath)
		nameWithoutExt := strings.TrimSuffix(baseName, filepath.Ext(baseName))
		outputBase := filepath.Join(tempDir, fmt.Sprintf("ocr_%d_%s", i, nameWithoutExt))

		// Tesseract automatically adds .pdf extension
		cmd := exec.Command("tesseract",
			imgPath,
			outputBase,
			"-l", language,
			"pdf",
		)
		if err := cmd.Run(); err != nil {
			log.Printf("[WARN] OCR failed for page %d: %v", i+1, err)
			// Create a non-OCR PDF from the image as fallback
			pdfFile := outputBase + ".pdf"
			if err := api.ImportImagesFile([]string{imgPath}, pdfFile, nil, nil); err != nil {
				return nil, fmt.Errorf("failed to process page %d: %w", i+1, err)
			}
			pdfFiles = append(pdfFiles, pdfFile)
		} else {
			pdfFiles = append(pdfFiles, outputBase+".pdf")
		}
	}

	// Step 4: Merge all OCR'd pages into final PDF
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

	log.Printf("[INFO] OCR complete: %d pages processed with language %s", len(imageFiles), language)
	return metadata, nil
}

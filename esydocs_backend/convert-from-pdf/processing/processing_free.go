package processing

import (
	"archive/zip"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// Free tool implementations using pdfcpu and system tools

func mergePDFs(inputPaths []string, outputPath string) error {
	if len(inputPaths) < 2 {
		return fmt.Errorf("merge requires at least 2 PDF files")
	}

	log.Printf("[INFO] Merging %d PDFs to %s", len(inputPaths), outputPath)
	return api.MergeCreateFile(inputPaths, outputPath, false, nil)
}

func splitPDF(inputPath string, outputPath string, pageRange string) error {
	log.Printf("[INFO] Splitting PDF %s with range %s", inputPath, pageRange)

	// Parse page range (e.g., "1-3,5,7-9")
	// pdfcpu expects a specific format
	// For now, extract all pages individually and zip them

	// Get page count
	ctx, err := api.ReadContextFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read PDF: %w", err)
	}

	pageCount := ctx.PageCount
	log.Printf("[INFO] PDF has %d pages", pageCount)

	// Create temp directory for split pages
	tempDir, err := os.MkdirTemp("", "pdf-split-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Split into individual pages
	for i := 1; i <= pageCount; i++ {
		outputFile := filepath.Join(tempDir, fmt.Sprintf("page_%03d.pdf", i))
		err := api.ExtractPagesFile(inputPath, outputFile, []string{fmt.Sprintf("%d", i)}, nil)
		if err != nil {
			log.Printf("[WARN] Failed to extract page %d: %v", i, err)
			continue
		}
	}

	// Create zip archive
	return zipDirectory(tempDir, outputPath)
}

func compressPDF(inputPath string, outputPath string) error {
	log.Printf("[INFO] Compressing PDF %s", inputPath)

	// Use pdfcpu optimize
	conf := model.NewDefaultConfiguration()
	conf.OptimizeDuplicateContentStreams = true

	return api.OptimizeFile(inputPath, outputPath, conf)
}

func encryptPDF(inputPath string, outputPath string, password string) error {
	log.Printf("[INFO] Encrypting PDF %s", inputPath)

	conf := model.NewDefaultConfiguration()
	conf.UserPW = password
	conf.OwnerPW = password // Use same password for simplicity

	return api.EncryptFile(inputPath, outputPath, conf)
}

func decryptPDF(inputPath string, outputPath string, password string) error {
	log.Printf("[INFO] Decrypting PDF %s", inputPath)

	conf := model.NewDefaultConfiguration()
	conf.UserPW = password

	return api.DecryptFile(inputPath, outputPath, conf)
}

func pdfToImages(inputPath string, outputPath string) error {
	log.Printf("[INFO] Converting PDF to images: %s", inputPath)

	// Create temp directory for images
	tempDir, err := os.MkdirTemp("", "pdf-images-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Use pdfcpu to render pages as images
	ctx, err := api.ReadContextFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read PDF: %w", err)
	}

	// Extract each page as image using pdftoppm (from poppler-utils)
	for i := 1; i <= ctx.PageCount; i++ {
		// pdftoppm creates files with pattern: prefix-N.png
		cmd := exec.Command("pdftoppm", "-png", "-f", fmt.Sprintf("%d", i), "-l", fmt.Sprintf("%d", i), inputPath, filepath.Join(tempDir, fmt.Sprintf("page_%03d", i)))
		if err := cmd.Run(); err != nil {
			log.Printf("[ERROR] pdftoppm failed: %v", err)
			return fmt.Errorf("PDF to image conversion requires pdftoppm (poppler-utils): %w", err)
		}
	}

	// Create zip archive
	return zipDirectory(tempDir, outputPath)
}

func officeToPDF(inputPath string, outputPath string, fileType string) error {
	log.Printf("[INFO] Converting %s to PDF: %s", fileType, inputPath)

	// Use LibreOffice in headless mode
	// libreoffice --headless --convert-to pdf --outdir <dir> <file>

	outputDir := filepath.Dir(outputPath)

	cmd := exec.Command("libreoffice", "--headless", "--convert-to", "pdf", "--outdir", outputDir, inputPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[ERROR] LibreOffice conversion failed: %s", string(output))
		return fmt.Errorf("LibreOffice not available or conversion failed: %w. Install with: apt-get install libreoffice", err)
	}

	// LibreOffice creates file with same name but .pdf extension
	convertedFile := filepath.Join(outputDir, strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))+".pdf")

	// Rename to expected output
	if convertedFile != outputPath {
		return os.Rename(convertedFile, outputPath)
	}

	return nil
}

// validateJavaRuntime checks if Java is available for LibreOffice export filters
func validateJavaRuntime() error {
	cmd := exec.Command("java", "-version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("Java Runtime Environment not found. LibreOffice requires Java for MS Office format conversions. Error: %w, Output: %s", err, string(output))
	}
	log.Printf("[DEBUG] Java runtime available: %s", string(output))
	return nil
}

func pdfToOffice(inputPath string, outputPath string, outputFormat string) error {
	log.Printf("[WARNING] Attempting PDF to %s conversion: %s", outputFormat, inputPath)

	// IMPORTANT: PDF to Office conversion is NOT reliably supported
	// LibreOffice can convert Office→PDF but PDF→Office is very limited
	// This feature is provided on a best-effort basis and will fail for most PDFs

	return fmt.Errorf("PDF to %s conversion is not supported. LibreOffice cannot convert PDF files to Office formats (docx, xlsx, pptx). This is a technical limitation - PDFs are 'final' output formats and cannot be reliably converted back to editable Office documents. Alternative: Use specialized PDF editing tools or OCR software for PDF to Office conversion", outputFormat)
}

func imageToPDF(inputPaths []string, outputPath string) error {
	log.Printf("[INFO] Converting %d images to PDF", len(inputPaths))

	// Use pdfcpu to create PDF from images
	return api.ImportImagesFile(inputPaths, outputPath, nil, nil)
}

// Helper: Create zip archive from directory
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
		defer file.Close()

		_, err = io.Copy(zipEntry, file)
		return err
	})
}

// pdfToPdfa converts a PDF to PDF/A format using Ghostscript
func pdfToPdfa(inputPath string, outputPath string) error {
	log.Printf("[INFO] Converting PDF to PDF/A: %s", inputPath)

	// Use Ghostscript to convert PDF to PDF/A-2b
	// gs -dPDFA=2 -dBATCH -dNOPAUSE -sProcessColorModel=DeviceRGB -sDEVICE=pdfwrite -sPDFACompatibilityPolicy=1 -sOutputFile=output.pdf input.pdf
	cmd := exec.Command("gs",
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
		log.Printf("[ERROR] Ghostscript PDF/A conversion failed: %s", string(output))
		return fmt.Errorf("Ghostscript not available or conversion failed: %w. Install with: apt-get install ghostscript", err)
	}

	log.Printf("[INFO] PDF/A conversion completed: %s", outputPath)
	return nil
}

// Watermark PDF
func watermarkPDF(inputPath string, outputPath string, watermarkText string) error {
	log.Printf("[INFO] Adding watermark to PDF: %s", inputPath)

	// Use pdfcpu watermark functionality
	// ParseTextWatermarkDetails signature: (text, desc string, onTop bool, unit DisplayUnit)
	wm, err := pdfcpu.ParseTextWatermarkDetails(watermarkText, "font:Helvetica, points:48, color:0.5 0.5 0.5, opacity:0.3, rotation:45", false, types.POINTS)
	if err != nil {
		return fmt.Errorf("failed to parse watermark: %w", err)
	}

	return api.AddWatermarksFile(inputPath, outputPath, nil, wm, nil)
}

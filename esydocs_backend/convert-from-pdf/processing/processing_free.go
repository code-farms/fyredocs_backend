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
)

// pdfToImages converts PDF pages to PNG images using pdftoppm (poppler-utils)
func pdfToImages(inputPath string, outputPath string) error {
	log.Printf("[INFO] Converting PDF to images: %s", inputPath)

	// Create temp directory for images
	tempDir, err := os.MkdirTemp("", "pdf-images-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Get page count using pdfcpu
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

// pdfToPdfa converts a PDF to PDF/A format using Ghostscript
func pdfToPdfa(inputPath string, outputPath string) error {
	log.Printf("[INFO] Converting PDF to PDF/A: %s", inputPath)

	// Use Ghostscript to convert PDF to PDF/A-2b
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
		return fmt.Errorf("Ghostscript not available or conversion failed: %w", err)
	}

	log.Printf("[INFO] PDF/A conversion completed: %s", outputPath)
	return nil
}

// pdfToOffice converts PDF to Office formats using LibreOffice
func pdfToOffice(inputPath string, outputPath string, outputFormat string) error {
	log.Printf("[INFO] Converting PDF to %s: %s", outputFormat, inputPath)

	outputDir := filepath.Dir(outputPath)

	// LibreOffice can import PDF and export to Office formats
	// Use the PDF import filter explicitly
	cmd := exec.Command("libreoffice",
		"--headless",
		"--infilter=writer_pdf_import",
		"--convert-to", outputFormat,
		"--outdir", outputDir,
		inputPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[ERROR] LibreOffice conversion failed: %s", string(output))
		return fmt.Errorf("PDF to %s conversion failed: %w", outputFormat, err)
	}

	// LibreOffice creates file with same base name but new extension
	baseName := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	convertedFile := filepath.Join(outputDir, baseName+"."+outputFormat)

	// Rename to expected output path
	if convertedFile != outputPath {
		if err := os.Rename(convertedFile, outputPath); err != nil {
			return fmt.Errorf("failed to rename output file: %w", err)
		}
	}

	log.Printf("[INFO] PDF to %s conversion completed: %s", outputFormat, outputPath)
	return nil
}

// pdfToHTML converts PDF to HTML using pdftohtml (poppler-utils)
func pdfToHTML(inputPath string, outputPath string) error {
	log.Printf("[INFO] Converting PDF to HTML: %s", inputPath)

	// Create temp directory for HTML output
	tempDir, err := os.MkdirTemp("", "pdf-html-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// pdftohtml creates multiple files (HTML + images)
	outputBase := filepath.Join(tempDir, "output")
	cmd := exec.Command("pdftohtml", "-s", "-noframes", inputPath, outputBase)

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[ERROR] pdftohtml failed: %s", string(output))
		return fmt.Errorf("PDF to HTML conversion failed: %w", err)
	}

	// Create zip archive with all HTML files and images
	return zipDirectory(tempDir, outputPath)
}

// pdfToText converts PDF to plain text using pdftotext (poppler-utils)
func pdfToText(inputPath string, outputPath string) error {
	log.Printf("[INFO] Converting PDF to text: %s", inputPath)

	cmd := exec.Command("pdftotext", "-layout", inputPath, outputPath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[ERROR] pdftotext failed: %s", string(output))
		return fmt.Errorf("PDF to text conversion failed: %w", err)
	}

	log.Printf("[INFO] PDF to text conversion completed: %s", outputPath)
	return nil
}

// zipDirectory creates a zip archive from directory contents
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

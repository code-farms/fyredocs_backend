package processing

import (
	"archive/zip"
	"fmt"
	"io"
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

// mergePDFs merges multiple PDF files into one
func mergePDFs(inputPaths []string, outputPath string) error {
	if len(inputPaths) < 2 {
		return fmt.Errorf("merge requires at least 2 PDF files")
	}

	log.Printf("[INFO] Merging %d PDFs to %s", len(inputPaths), outputPath)
	return api.MergeCreateFile(inputPaths, outputPath, false, nil)
}

// splitPDF splits a PDF into individual pages or page ranges
func splitPDF(inputPath string, outputPath string, pageRange string) error {
	log.Printf("[INFO] Splitting PDF %s with range %s", inputPath, pageRange)

	ctx, err := api.ReadContextFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read PDF: %w", err)
	}

	pageCount := ctx.PageCount
	log.Printf("[INFO] PDF has %d pages", pageCount)

	tempDir, err := os.MkdirTemp("", "pdf-split-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Parse page range
	pages := parsePageRange(pageRange, pageCount)
	if len(pages) == 0 {
		return fmt.Errorf("invalid page range: %s", pageRange)
	}

	// Split into individual pages
	for _, pageNum := range pages {
		outputFile := filepath.Join(tempDir, fmt.Sprintf("page_%03d.pdf", pageNum))
		err := api.ExtractPagesFile(inputPath, outputFile, []string{fmt.Sprintf("%d", pageNum)}, nil)
		if err != nil {
			log.Printf("[WARN] Failed to extract page %d: %v", pageNum, err)
			continue
		}
	}

	return zipDirectory(tempDir, outputPath)
}

// removePages removes specified pages from a PDF
func removePages(inputPath string, outputPath string, pages string) error {
	log.Printf("[INFO] Removing pages %s from PDF %s", pages, inputPath)

	ctx, err := api.ReadContextFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read PDF: %w", err)
	}

	pageCount := ctx.PageCount
	pagesToRemove := parsePageRange(pages, pageCount)
	if len(pagesToRemove) == 0 {
		return fmt.Errorf("invalid pages specification: %s", pages)
	}

	// Convert to string slice for pdfcpu
	pageStrings := make([]string, len(pagesToRemove))
	for i, p := range pagesToRemove {
		pageStrings[i] = fmt.Sprintf("%d", p)
	}

	return api.RemovePagesFile(inputPath, outputPath, pageStrings, nil)
}

// extractPages extracts specified pages from a PDF
func extractPages(inputPath string, outputPath string, pages string) error {
	log.Printf("[INFO] Extracting pages %s from PDF %s", pages, inputPath)

	ctx, err := api.ReadContextFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read PDF: %w", err)
	}

	pageCount := ctx.PageCount
	pagesToExtract := parsePageRange(pages, pageCount)
	if len(pagesToExtract) == 0 {
		return fmt.Errorf("invalid pages specification: %s", pages)
	}

	// Convert to string slice for pdfcpu
	pageStrings := make([]string, len(pagesToExtract))
	for i, p := range pagesToExtract {
		pageStrings[i] = fmt.Sprintf("%d", p)
	}

	return api.ExtractPagesFile(inputPath, outputPath, pageStrings, nil)
}

// organizePDF reorders pages in a PDF according to specified order
func organizePDF(inputPath string, outputPath string, order string) error {
	log.Printf("[INFO] Organizing PDF %s with order %s", inputPath, order)

	ctx, err := api.ReadContextFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read PDF: %w", err)
	}

	pageCount := ctx.PageCount
	pageOrder := parsePageOrder(order, pageCount)
	if len(pageOrder) == 0 {
		return fmt.Errorf("invalid page order: %s", order)
	}

	// Create temp directory for extracted pages
	tempDir, err := os.MkdirTemp("", "pdf-organize-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Extract pages in the specified order
	tempFiles := make([]string, len(pageOrder))
	for i, pageNum := range pageOrder {
		tempFile := filepath.Join(tempDir, fmt.Sprintf("page_%03d.pdf", i))
		err := api.ExtractPagesFile(inputPath, tempFile, []string{fmt.Sprintf("%d", pageNum)}, nil)
		if err != nil {
			return fmt.Errorf("failed to extract page %d: %w", pageNum, err)
		}
		tempFiles[i] = tempFile
	}

	// Merge pages in new order
	return api.MergeCreateFile(tempFiles, outputPath, false, nil)
}

// scanToPDF converts images to PDF (similar to scanning documents)
func scanToPDF(inputPaths []string, outputPath string, options map[string]interface{}) error {
	log.Printf("[INFO] Converting %d images to PDF (scan-to-pdf)", len(inputPaths))

	if len(inputPaths) == 0 {
		return fmt.Errorf("no input images provided")
	}

	// Check if OCR is requested
	ocr, _ := options["ocr"].(bool)
	if ocr {
		return scanToPDFWithOCR(inputPaths, outputPath)
	}

	// Use pdfcpu to create PDF from images
	conf := model.NewDefaultConfiguration()
	return api.ImportImagesFile(inputPaths, outputPath, nil, conf)
}

// scanToPDFWithOCR converts images to searchable PDF using OCR
func scanToPDFWithOCR(inputPaths []string, outputPath string) error {
	log.Printf("[INFO] Converting images to searchable PDF using OCR")

	// Check if tesseract is available
	if _, err := exec.LookPath("tesseract"); err != nil {
		return fmt.Errorf("OCR requires tesseract to be installed. Install with: apt-get install tesseract-ocr")
	}

	tempDir, err := os.MkdirTemp("", "pdf-ocr-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Process each image with OCR
	pdfFiles := make([]string, len(inputPaths))
	for i, imgPath := range inputPaths {
		baseName := filepath.Base(imgPath)
		nameWithoutExt := strings.TrimSuffix(baseName, filepath.Ext(baseName))
		outputBase := filepath.Join(tempDir, fmt.Sprintf("ocr_%d_%s", i, nameWithoutExt))

		// Tesseract automatically adds .pdf extension
		cmd := exec.Command("tesseract", imgPath, outputBase, "-l", "eng", "pdf")
		if err := cmd.Run(); err != nil {
			log.Printf("[WARN] OCR failed for %s: %v", imgPath, err)
			// Fallback to non-OCR conversion for this page
			pdfFile := outputBase + ".pdf"
			if err := api.ImportImagesFile([]string{imgPath}, pdfFile, nil, nil); err != nil {
				return fmt.Errorf("failed to process image %s: %w", imgPath, err)
			}
			pdfFiles[i] = pdfFile
		} else {
			pdfFiles[i] = outputBase + ".pdf"
		}
	}

	// Merge all PDFs
	if len(pdfFiles) == 1 {
		return copyFile(pdfFiles[0], outputPath)
	}
	return api.MergeCreateFile(pdfFiles, outputPath, false, nil)
}

// parsePageRange parses page range strings like "1-3,5,7-9" or "all"
func parsePageRange(rangeStr string, maxPages int) []int {
	rangeStr = strings.TrimSpace(rangeStr)
	if rangeStr == "" || rangeStr == "all" {
		pages := make([]int, maxPages)
		for i := 0; i < maxPages; i++ {
			pages[i] = i + 1
		}
		return pages
	}

	var pages []int
	seenPages := make(map[int]bool)
	parts := strings.Split(rangeStr, ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.Contains(part, "-") {
			rangeParts := strings.Split(part, "-")
			if len(rangeParts) != 2 {
				continue
			}
			start, err1 := strconv.Atoi(strings.TrimSpace(rangeParts[0]))
			end, err2 := strconv.Atoi(strings.TrimSpace(rangeParts[1]))
			if err1 != nil || err2 != nil || start < 1 || end > maxPages || start > end {
				continue
			}
			for i := start; i <= end; i++ {
				if !seenPages[i] {
					pages = append(pages, i)
					seenPages[i] = true
				}
			}
		} else {
			page, err := strconv.Atoi(part)
			if err != nil || page < 1 || page > maxPages {
				continue
			}
			if !seenPages[page] {
				pages = append(pages, page)
				seenPages[page] = true
			}
		}
	}

	sort.Ints(pages)
	return pages
}

// parsePageOrder parses page order strings like "3,1,2,4" or "1-4"
func parsePageOrder(order string, maxPages int) []int {
	return parsePageRange(order, maxPages)
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

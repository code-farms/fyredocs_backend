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
	"sort"
	"strconv"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

func mergePDFs(inputPaths []string, outputPath string) error {
	if len(inputPaths) < 2 {
		return fmt.Errorf("merge requires at least 2 PDF files")
	}
	slog.Info("merging PDFs", "count", len(inputPaths), "output", outputPath)
	return api.MergeCreateFile(inputPaths, outputPath, false, nil)
}

func splitPDF(inputPath string, outputPath string, mode string, rangeValue string) error {
	slog.Info("splitting PDF", "input", inputPath, "mode", mode, "range", rangeValue)

	ctx, err := api.ReadContextFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read PDF: %w", err)
	}

	pageCount := ctx.PageCount
	slog.Info("PDF page count", "pages", pageCount)

	tempDir, err := os.MkdirTemp("", "pdf-split-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	switch mode {
	case "range":
		// Each range group becomes one multi-page PDF.
		groups := parsePageRangeGroups(rangeValue, pageCount)
		if len(groups) == 0 {
			return fmt.Errorf("invalid page range: %s", rangeValue)
		}
		for i, group := range groups {
			outFile := filepath.Join(tempDir, fmt.Sprintf("pages_%03d.pdf", i+1))
			pageStrings := make([]string, len(group))
			for j, p := range group {
				pageStrings[j] = fmt.Sprintf("%d", p)
			}
			if err := api.CollectFile(inputPath, outFile, pageStrings, nil); err != nil {
				return fmt.Errorf("failed to collect pages %v: %w", group, err)
			}
		}

	case "extract":
		// Split into chunks of N pages each.
		span, err := strconv.Atoi(strings.TrimSpace(rangeValue))
		if err != nil || span < 1 {
			return fmt.Errorf("invalid span value: %s", rangeValue)
		}
		if err := api.SplitFile(inputPath, tempDir, span, nil); err != nil {
			return fmt.Errorf("failed to split PDF by span %d: %w", span, err)
		}

	case "equal":
		// Divide into N equal parts.
		parts, err := strconv.Atoi(strings.TrimSpace(rangeValue))
		if err != nil || parts < 2 {
			return fmt.Errorf("invalid number of parts: %s", rangeValue)
		}
		if parts > pageCount {
			parts = pageCount
		}
		groups := splitEqualGroups(pageCount, parts)
		for i, group := range groups {
			outFile := filepath.Join(tempDir, fmt.Sprintf("part_%03d.pdf", i+1))
			pageStrings := make([]string, len(group))
			for j, p := range group {
				pageStrings[j] = fmt.Sprintf("%d", p)
			}
			if err := api.CollectFile(inputPath, outFile, pageStrings, nil); err != nil {
				return fmt.Errorf("failed to collect part %d: %w", i+1, err)
			}
		}

	default:
		// "all" or empty: extract each page as an individual PDF.
		pages := parsePageRange(rangeValue, pageCount)
		if len(pages) == 0 {
			pages = parsePageRange("all", pageCount)
		}
		pageStrings := make([]string, len(pages))
		for i, p := range pages {
			pageStrings[i] = fmt.Sprintf("%d", p)
		}
		if err := api.ExtractPagesFile(inputPath, tempDir, pageStrings, nil); err != nil {
			return fmt.Errorf("failed to extract pages: %w", err)
		}
	}

	return zipDirectory(tempDir, outputPath)
}

func removePages(inputPath string, outputPath string, pages string) error {
	slog.Info("removing pages from PDF", "pages", pages, "input", inputPath)

	ctx, err := api.ReadContextFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read PDF: %w", err)
	}

	pageCount := ctx.PageCount
	pagesToRemove := parsePageRange(pages, pageCount)
	if len(pagesToRemove) == 0 {
		return fmt.Errorf("invalid pages specification: %s", pages)
	}

	pageStrings := make([]string, len(pagesToRemove))
	for i, p := range pagesToRemove {
		pageStrings[i] = fmt.Sprintf("%d", p)
	}

	return api.RemovePagesFile(inputPath, outputPath, pageStrings, nil)
}

func extractPages(inputPath string, outputPath string, pages string) error {
	slog.Info("extracting pages from PDF", "pages", pages, "input", inputPath)

	ctx, err := api.ReadContextFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read PDF: %w", err)
	}

	pageCount := ctx.PageCount
	pagesToExtract := parsePageRange(pages, pageCount)
	if len(pagesToExtract) == 0 {
		return fmt.Errorf("invalid pages specification: %s", pages)
	}

	pageStrings := make([]string, len(pagesToExtract))
	for i, p := range pagesToExtract {
		pageStrings[i] = fmt.Sprintf("%d", p)
	}

	return api.CollectFile(inputPath, outputPath, pageStrings, nil)
}

func organizePDF(inputPath string, outputPath string, order string) error {
	slog.Info("organizing PDF", "input", inputPath, "order", order)

	ctx, err := api.ReadContextFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read PDF: %w", err)
	}

	pageCount := ctx.PageCount
	pageOrder := parsePageOrder(order, pageCount)
	if len(pageOrder) == 0 {
		return fmt.Errorf("invalid page order: %s", order)
	}

	tempDir, err := os.MkdirTemp("", "pdf-organize-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	tempFiles := make([]string, len(pageOrder))
	for i, pageNum := range pageOrder {
		tempFile := filepath.Join(tempDir, fmt.Sprintf("page_%03d.pdf", i))
		err := api.ExtractPagesFile(inputPath, tempFile, []string{fmt.Sprintf("%d", pageNum)}, nil)
		if err != nil {
			return fmt.Errorf("failed to extract page %d: %w", pageNum, err)
		}
		tempFiles[i] = tempFile
	}

	return api.MergeCreateFile(tempFiles, outputPath, false, nil)
}

func scanToPDF(ctx context.Context, inputPaths []string, outputPath string, options map[string]interface{}) error {
	slog.Info("converting images to PDF (scan-to-pdf)", "count", len(inputPaths))

	if len(inputPaths) == 0 {
		return fmt.Errorf("no input images provided")
	}

	ocr, _ := options["ocr"].(bool)
	if ocr {
		return scanToPDFWithOCR(ctx, inputPaths, outputPath)
	}

	conf := model.NewDefaultConfiguration()
	return api.ImportImagesFile(inputPaths, outputPath, nil, conf)
}

func scanToPDFWithOCR(ctx context.Context, inputPaths []string, outputPath string) error {
	slog.Info("converting images to searchable PDF using OCR")

	if _, err := exec.LookPath("tesseract"); err != nil {
		return fmt.Errorf("OCR requires tesseract to be installed")
	}

	tempDir, err := os.MkdirTemp("", "pdf-ocr-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	pdfFiles := make([]string, len(inputPaths))
	for i, imgPath := range inputPaths {
		baseName := filepath.Base(imgPath)
		nameWithoutExt := strings.TrimSuffix(baseName, filepath.Ext(baseName))
		outputBase := filepath.Join(tempDir, fmt.Sprintf("ocr_%d_%s", i, nameWithoutExt))

		cmd := exec.CommandContext(ctx, "tesseract", imgPath, outputBase, "-l", "eng", "pdf")
		if err := cmd.Run(); err != nil {
			slog.Warn("OCR failed, falling back to non-OCR", "image", imgPath, "error", err)
			pdfFile := outputBase + ".pdf"
			if err := api.ImportImagesFile([]string{imgPath}, pdfFile, nil, nil); err != nil {
				return fmt.Errorf("failed to process image %s: %w", imgPath, err)
			}
			pdfFiles[i] = pdfFile
		} else {
			pdfFiles[i] = outputBase + ".pdf"
		}
	}

	if len(pdfFiles) == 1 {
		return copyFile(pdfFiles[0], outputPath)
	}
	return api.MergeCreateFile(pdfFiles, outputPath, false, nil)
}

func rotatePDF(inputPath string, outputPath string, rotation int, applyTo string) error {
	slog.Info("rotating PDF", "input", inputPath, "rotation", rotation, "applyTo", applyTo)

	var pageSelection []string
	switch applyTo {
	case "odd":
		pageSelection = []string{"odd"}
	case "even":
		pageSelection = []string{"even"}
	default: // "all" or empty
		pageSelection = nil
	}

	return api.RotateFile(inputPath, outputPath, rotation, pageSelection, nil)
}

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

// parsePageRangeGroups parses a range string like "1-3,5,7-10" into groups
// of page numbers: [[1,2,3], [5], [7,8,9,10]]. Each group becomes one PDF.
func parsePageRangeGroups(rangeStr string, maxPages int) [][]int {
	rangeStr = strings.TrimSpace(rangeStr)
	if rangeStr == "" || rangeStr == "all" {
		// "all" as a single group containing every page.
		pages := make([]int, maxPages)
		for i := 0; i < maxPages; i++ {
			pages[i] = i + 1
		}
		return [][]int{pages}
	}

	var groups [][]int
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
			group := make([]int, 0, end-start+1)
			for i := start; i <= end; i++ {
				group = append(group, i)
			}
			groups = append(groups, group)
		} else {
			page, err := strconv.Atoi(part)
			if err != nil || page < 1 || page > maxPages {
				continue
			}
			groups = append(groups, []int{page})
		}
	}

	return groups
}

// splitEqualGroups divides pageCount pages into n roughly equal groups.
// For example, splitEqualGroups(10, 4) returns [[1,2,3],[4,5,6],[7,8],[9,10]].
func splitEqualGroups(pageCount int, n int) [][]int {
	if n <= 0 {
		return nil
	}
	if n > pageCount {
		n = pageCount
	}
	groups := make([][]int, n)
	base := pageCount / n
	extra := pageCount % n
	page := 1
	for i := 0; i < n; i++ {
		size := base
		if i < extra {
			size++
		}
		group := make([]int, size)
		for j := 0; j < size; j++ {
			group[j] = page
			page++
		}
		groups[i] = group
	}
	return groups
}

func parsePageOrder(order string, maxPages int) []int {
	return parsePageRange(order, maxPages)
}

func zipDirectory(sourceDir string, zipPath string) error {
	zipFile, err := os.Create(zipPath)
	if err != nil {
		return fmt.Errorf("failed to create zip: %w", err)
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	fileCount := 0
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
		fileCount++
		return closeErr
	}); err != nil {
		return err
	}

	if fileCount == 0 {
		return fmt.Errorf("no files to archive in %s", sourceDir)
	}
	return nil
}

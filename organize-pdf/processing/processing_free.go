package processing

import (
	"archive/zip"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// mergePDFs concatenates inputs in the order given. Input order is the user's
// chosen order from the UI, so it must be preserved exactly rather than sorted.
func mergePDFs(inputPaths []string, outputPath string) error {
	if len(inputPaths) < 2 {
		return fmt.Errorf("merge requires at least 2 PDF files")
	}
	slog.Info("merging PDFs", "count", len(inputPaths), "output", outputPath)
	return api.MergeCreateFile(inputPaths, outputPath, false, nil)
}

// splitPDF divides a PDF into multiple files and returns them as a single ZIP,
// because the job model has exactly one output object per job.
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

// removePages deletes the pages named by the caller's range expression. Page
// numbers are 1-based throughout the PDF tools, matching what the user sees.
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

// extractPages keeps only the pages named by the range expression — the
// complement of removePages.
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

// organizePDF reorders pages into an explicitly supplied sequence, which may
// repeat or omit pages. This is the general case that rotate/remove/extract are
// specializations of.
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

	pageStrings := make([]string, len(pageOrder))
	for i, p := range pageOrder {
		pageStrings[i] = fmt.Sprintf("%d", p)
	}

	return api.CollectFile(inputPath, outputPath, pageStrings, nil)
}

// rotatePDF rotates pages by a multiple of 90 degrees. Rotation is recorded as
// page metadata rather than by re-rendering content, so it is lossless and fast.
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

// parsePageRange expands a page expression like "1,3,5-8" into 1-based page
// numbers, deduplicated and sorted ascending — input order is NOT preserved
// (callers needing user-specified order use parsePageRangeGroups). An empty
// string or "all" means every page.
//
// Malformed or out-of-bounds components are SKIPPED rather than reported: a
// reversed range, an unparseable number, or a page past the end simply
// contributes nothing. That is deliberate for a user-facing selection — "1-5" on
// a 3-page document should act on what exists instead of failing — but it means
// this function cannot be used to validate input, because it never reports what
// it ignored. A caller needing "the user asked for something impossible" must
// compare the result length itself, and an expression naming no valid page yields
// an empty slice.
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

// parsePageOrder parses an explicit page sequence for organizePDF. Unlike
// parsePageRange this preserves duplicates and order, since repeating a page is a
// legitimate reordering request.
func parsePageOrder(order string, maxPages int) []int {
	return parsePageRange(order, maxPages)
}

// zipDirectory archives every file under sourceDir into a ZIP, using paths
// relative to sourceDir so the archive has no absolute-path entries.
//
// Directories are skipped rather than added as entries; ZIP readers recreate them
// implicitly from file paths. Only used for scratch directories this process just
// produced, which is why symlinks are not specially handled — there are none.
func zipDirectory(sourceDir string, zipPath string) error {
	zipFile, err := os.Create(zipPath)
	if err != nil {
		return fmt.Errorf("failed to create zip: %w", err)
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)

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
		zipWriter.Close()
		return err
	}

	if fileCount == 0 {
		zipWriter.Close()
		return fmt.Errorf("no files to archive in %s", sourceDir)
	}

	if err := zipWriter.Close(); err != nil {
		return fmt.Errorf("failed to finalize zip: %w", err)
	}
	return nil
}

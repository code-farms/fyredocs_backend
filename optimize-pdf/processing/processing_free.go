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

// ocrMaxWorkers returns the OCR page-worker pool size. It defaults to
// min(NumCPU, 4) to bound concurrent tesseract memory on small hosts, and is
// overridable via OCR_MAX_WORKERS (values < 1 clamp to 1) so larger boxes can
// use more cores for multi-page scans.
func ocrMaxWorkers() int {
	if v := os.Getenv("OCR_MAX_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			if n < 1 {
				return 1
			}
			return n
		}
	}
	workers := runtime.NumCPU()
	if workers > 4 {
		workers = 4
	}
	if workers < 1 {
		workers = 1
	}
	return workers
}

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

	args := buildCompressArgs(quality, outputPath, inputPath)

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

// buildCompressArgs returns the Ghostscript arguments for the given quality level.
func buildCompressArgs(quality, outputPath, inputPath string) []string {
	pdfSettings := "/ebook"
	dpi := "150"
	threshold := "1.5"
	qFactor := ""
	var extraArgs []string

	switch quality {
	case "low":
		pdfSettings = "/printer"
		dpi = "300"
	case "medium":
		pdfSettings = "/ebook"
		dpi = "150"
	case "high":
		pdfSettings = "/ebook"
		dpi = "72"
		threshold = "1.0"
		qFactor = "0.76"
	case "extreme":
		pdfSettings = "/screen"
		dpi = "36"
		threshold = "1.0"
		qFactor = "2.4"
		extraArgs = []string{
			"-dColorConversionStrategy=/Gray",
			"-dProcessColorModel=/DeviceGray",
		}
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
		"-dColorImageDownsampleThreshold=" + threshold,
		"-dDownsampleGrayImages=true",
		"-dGrayImageResolution=" + dpi,
		"-dGrayImageDownsampleThreshold=" + threshold,
		"-dDownsampleMonoImages=true",
		"-dMonoImageResolution=" + dpi,
		"-dMonoImageDownsampleThreshold=" + threshold,
	}
	args = append(args, extraArgs...)
	if qFactor != "" {
		args = append(args,
			"-c",
			fmt.Sprintf("<< /ColorACSImageDict << /QFactor %s >> /GrayACSImageDict << /QFactor %s >> >> setdistillerparams", qFactor, qFactor),
			"-f",
		)
	}
	args = append(args, "-sOutputFile="+outputPath, inputPath)
	return args
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

// ocrMaxImageDim returns the maximum allowed pixel length for the longest edge
// of a rasterized OCR page. Rendering a large page at a high DPI can produce an
// image big enough to make tesseract fail (and exhaust memory); capping the
// effective DPI to this bound keeps OCR reliable. Overridable via
// OCR_MAX_IMAGE_DIM (values < 1 fall back to the default).
func ocrMaxImageDim() int {
	const def = 10000
	if v := os.Getenv("OCR_MAX_IMAGE_DIM"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// computeSafeDPI returns a render resolution that keeps the longest edge of the
// output image within maxDim pixels, never exceeding requestedDPI. Page
// dimensions are in PDF points (1 point = 1/72 inch), so pixels = pts/72 * dpi.
// If page dimensions are unknown (<= 0) or maxDim <= 0, requestedDPI is returned
// unchanged. The size bound takes precedence over resolution: an extremely large
// page may be rendered below the requested DPI (down to a floor of 1) so OCR
// still succeeds rather than failing on an oversized image.
func computeSafeDPI(widthPts, heightPts float64, requestedDPI, maxDim int) int {
	longest := widthPts
	if heightPts > longest {
		longest = heightPts
	}
	if longest <= 0 || maxDim <= 0 {
		return requestedDPI
	}

	fitDPI := int(float64(maxDim) * 72.0 / longest)
	if fitDPI >= requestedDPI {
		return requestedDPI
	}
	if fitDPI < 1 {
		return 1
	}
	return fitDPI
}

// pdfPageDimensions returns the media-box width and height (in PDF points) of the
// document's first page by parsing `pdfinfo` output (poppler-utils). It is used
// only to bound the render DPI, so callers treat errors as non-fatal.
func pdfPageDimensions(ctx context.Context, inputPath string) (float64, float64, error) {
	out, err := exec.CommandContext(ctx, "pdfinfo", inputPath).Output()
	if err != nil {
		return 0, 0, fmt.Errorf("pdfinfo failed: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "Page size:") {
			continue
		}
		// Example: "Page size:      1224 x 1584 pts (letter)"
		rest := strings.TrimSpace(strings.TrimPrefix(line, "Page size:"))
		fields := strings.Fields(rest)
		if len(fields) < 3 || fields[1] != "x" {
			break
		}
		w, errW := strconv.ParseFloat(fields[0], 64)
		h, errH := strconv.ParseFloat(fields[2], 64)
		if errW != nil || errH != nil {
			break
		}
		return w, h, nil
	}
	return 0, 0, fmt.Errorf("could not parse page size from pdfinfo output")
}

// ocrLangMap maps ISO 639-1 codes (as the frontend sends) to tesseract's ISO
// 639-2/T codes.
var ocrLangMap = map[string]string{
	"en": "eng", "es": "spa", "fr": "fra", "de": "deu",
	"it": "ita", "pt": "por", "zh": "chi_sim", "ja": "jpn",
	"ko": "kor", "ar": "ara",
}

// ocrAllowedLanguages is the whitelist of tesseract language codes OCR will pass
// to `tesseract -l`. Anything outside it (an unmapped or crafted option value) is
// coerced to the default so an attacker-influenced language can never reach the
// tesseract invocation verbatim (finding I1). Extend this if more language packs
// are installed in the image.
var ocrAllowedLanguages = map[string]bool{
	"eng": true, "spa": true, "fra": true, "deu": true, "ita": true,
	"por": true, "chi_sim": true, "jpn": true, "kor": true, "ara": true,
}

// sanitizeOCRLanguage maps a requested language (ISO 639-1 or a tesseract code) to
// a whitelisted tesseract code, falling back to "eng" for anything unrecognized or
// empty. This is the trust boundary for the user-supplied "language" option.
func sanitizeOCRLanguage(requested string) string {
	requested = strings.TrimSpace(requested)
	if mapped, ok := ocrLangMap[requested]; ok {
		requested = mapped
	}
	if ocrAllowedLanguages[requested] {
		return requested
	}
	return "eng"
}

// resolveLanguage picks a tesseract language that is actually installed. The
// requested code is already mapped to tesseract's ISO 639-2 form (e.g. "eng").
// If it is available it is used as-is; otherwise it falls back to "eng", then to
// the first available language, and finally to the requested value so tesseract
// can surface its own error if nothing is installed.
func resolveLanguage(requested string, available []string) string {
	if len(available) == 0 {
		return requested
	}
	has := func(lang string) bool {
		for _, a := range available {
			if a == lang {
				return true
			}
		}
		return false
	}
	if has(requested) {
		return requested
	}
	if has("eng") {
		return "eng"
	}
	return available[0]
}

// availableTesseractLangs returns the languages tesseract can OCR with, parsed
// from `tesseract --list-langs`. Errors yield an empty slice (callers then keep
// the requested language).
func availableTesseractLangs(ctx context.Context) []string {
	out, err := exec.CommandContext(ctx, "tesseract", "--list-langs").CombinedOutput()
	if err != nil {
		return nil
	}
	var langs []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		// Skip the header line and any blank lines.
		if line == "" || strings.HasPrefix(line, "List of available languages") {
			continue
		}
		langs = append(langs, line)
	}
	return langs
}

func ocrPDF(ctx context.Context, inputPath string, outputPath string, options map[string]interface{}) (map[string]interface{}, error) {
	slog.Info("adding OCR layer to PDF", "input", inputPath)

	if _, err := exec.LookPath("pdftoppm"); err != nil {
		return nil, fmt.Errorf("pdftoppm not found. Install with: apk add poppler-utils")
	}
	if _, err := exec.LookPath("tesseract"); err != nil {
		return nil, fmt.Errorf("tesseract not found. Install with: apk add tesseract-ocr")
	}

	language, _ := optionString(options, "language")
	if language == "" {
		language = os.Getenv("OCR_DEFAULT_LANGUAGE")
	}
	// Map ISO 639-1 → tesseract codes and whitelist the result: only known codes
	// may reach `tesseract -l`; any unmapped/crafted value becomes "eng" (I1).
	language = sanitizeOCRLanguage(language)

	// Fall back to an installed language if the requested pack is missing, so a
	// missing language pack degrades gracefully instead of failing the whole job.
	if resolved := resolveLanguage(language, availableTesseractLangs(ctx)); resolved != language {
		slog.Warn("requested OCR language unavailable, falling back", "requested", language, "using", resolved)
		language = resolved
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

	// Cap the effective DPI so large pages don't rasterize into images big enough
	// to make tesseract (and the pdfcpu fallback) fail or exhaust memory.
	if w, h, err := pdfPageDimensions(ctx, inputPath); err == nil {
		if safe := computeSafeDPI(w, h, dpi, ocrMaxImageDim()); safe != dpi {
			slog.Info("capping OCR render DPI to bound image size", "requested", dpi, "using", safe, "widthPts", w, "heightPts", h)
			dpi = safe
		}
	} else {
		slog.Warn("could not determine PDF page size; using requested DPI", "error", err)
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

	// Process pages in parallel using a worker pool. Defaults to a CPU-sized
	// pool capped at 4 to bound tesseract memory on small hosts; OCR_MAX_WORKERS
	// raises (or lowers) that cap on larger boxes where memory is not the
	// constraint.
	workers := ocrMaxWorkers()

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

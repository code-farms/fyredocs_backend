package processing

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"fyredocs/shared/jobslots"
	"fyredocs/shared/procexec"
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

// compressPDF re-renders a PDF through Ghostscript at a quality preset.
//
// Ghostscript is a subprocess, so stderr is captured and logged but never
// returned to the user — its output names internal paths and tool versions.
// stdout is discarded because the result is written to outputPath directly.
// Compression is lossy for images: the returned metadata reports original and
// final size so the caller can tell the user what was actually saved, and can
// detect the case where "compression" made the file larger.
func compressPDF(ctx context.Context, inputPath string, outputPath string, options map[string]interface{}) (map[string]interface{}, error) {
	slog.Info("compressing PDF", "input", inputPath)

	inputInfo, err := os.Stat(inputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat input file: %w", err)
	}
	originalSize := inputInfo.Size()

	quality, _ := optionString(options, "quality")

	guardMeta, err := runGuardedRewrite(ctx, rewrite{
		tool:         "compress-pdf",
		args:         func(in string) []string { return buildCompressArgs(quality, outputPath, in) },
		textFallback: optimizeLossless,
	}, inputPath, outputPath)
	if err != nil {
		return nil, err
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
	for k, v := range guardMeta {
		metadata[k] = v
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

// repairPDF rewrites a structurally damaged PDF by passing it through
// Ghostscript, which parses leniently and emits a clean file.
//
// This works precisely because Ghostscript reconstructs rather than patches, so a
// broken cross-reference table or truncated trailer is rebuilt. It is not
// lossless: content Ghostscript cannot interpret is dropped, which is the
// intended trade for a file that opens at all.
func repairPDF(ctx context.Context, inputPath string, outputPath string) (map[string]interface{}, error) {
	slog.Info("repairing PDF using Ghostscript", "input", inputPath)

	meta, err := runGuardedRewrite(ctx, rewrite{
		tool:         "repair-pdf",
		args:         func(in string) []string { return buildRepairArgs(outputPath, in) },
		textFallback: optimizeLossless,
	}, inputPath, outputPath)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("repair failed: output file not created")
	}

	slog.Info("PDF repair complete", "output", outputPath)
	return meta, nil
}

// buildRepairArgs returns the Ghostscript arguments for repair: a lenient
// re-parse written out as a clean /prepress PDF.
func buildRepairArgs(outputPath, inputPath string) []string {
	return []string{
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
}

// buildFlattenArgs returns the Ghostscript arguments that bake annotation and
// form-field appearance streams into page content. -dPreserveAnnots=false is
// the flattening switch; -dPrinted makes annotations render their PRINT
// appearance, so hidden/no-print annotations are correctly excluded rather
// than baked in.
func buildFlattenArgs(outputPath, inputPath string) []string {
	return []string{
		"-dNOPAUSE",
		"-dBATCH",
		"-dSAFER",
		"-sDEVICE=pdfwrite",
		"-dCompatibilityLevel=1.4",
		"-dPreserveAnnots=false",
		"-dPrinted",
		"-dDetectDuplicateImages=true",
		"-dCompressFonts=true",
		fmt.Sprintf("-sOutputFile=%s", outputPath),
		inputPath,
	}
}

// flattenPDF re-distills a PDF through pdfwrite with annotation preservation
// disabled, so filled form data and markup become immutable page content
// while text stays vector/searchable. Deliberately NOT the rasterize route:
// rendering pages to images would destroy selection and balloon file size.
// Known limit: a form field with no appearance stream (rare NeedAppearances
// documents) renders nothing rather than its value.
func flattenPDF(ctx context.Context, inputPath string, outputPath string) (map[string]interface{}, error) {
	slog.Info("flattening PDF using Ghostscript", "input", inputPath)

	// No text fallback: a lossless rewrite would not flatten anything, so the
	// flattened output is kept and the job carries a warning instead.
	meta, err := runGuardedRewrite(ctx, rewrite{
		tool: "flatten-pdf",
		args: func(in string) []string { return buildFlattenArgs(outputPath, in) },
	}, inputPath, outputPath)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("flatten failed: output file not created")
	}

	slog.Info("PDF flatten complete", "output", outputPath)
	return meta, nil
}

// findGhostscript locates the Ghostscript binary, trying the Linux names first
// and then the Windows ones so a developer on Windows can run the tool locally
// even though production is Alpine.
//
// The error text names the Alpine install command because that is what a reader
// hitting this in a container needs. Resolution happens per call rather than being
// cached — LookPath is cheap next to spawning Ghostscript, and caching would
// survive a container-image change mid-process.
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
	out, err := procexec.Command(ctx, "pdfinfo", inputPath).Output()
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

// pdfPageCount returns the page count poppler's pdfinfo reports for inputPath.
// Poppler is also what renders the pages, so a PDF counts here exactly when
// pdftoppm can render it; pdfcpu rejects some damaged files poppler accepts.
func pdfPageCount(ctx context.Context, inputPath string) (int, error) {
	out, err := procexec.Command(ctx, "pdfinfo", inputPath).Output()
	if err != nil {
		return 0, fmt.Errorf("pdfinfo failed: %w", err)
	}
	return parsePdfinfoPages(string(out))
}

// parsePdfinfoPages extracts the "Pages:" value from pdfinfo output. Zero pages
// is an error: there would be nothing to OCR.
func parsePdfinfoPages(out string) (int, error) {
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "Pages:") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Pages:")))
		if err != nil {
			return 0, fmt.Errorf("could not parse page count from pdfinfo: %w", err)
		}
		if n < 1 {
			return 0, fmt.Errorf("PDF has no pages")
		}
		return n, nil
	}
	return 0, fmt.Errorf("pdfinfo reported no page count")
}

// ocrRenderArgs returns pdftoppm arguments that render exactly one page (1-based)
// of inputPath at dpi to <outBase>.png. -singlefile drops pdftoppm's page-number
// suffix, so the caller knows the file name without globbing.
func ocrRenderArgs(dpi, page int, inputPath, outBase string) []string {
	p := strconv.Itoa(page)
	return []string{"-png", "-r", strconv.Itoa(dpi), "-f", p, "-l", p, "-singlefile", inputPath, outBase}
}

// ocrLangMap maps ISO 639-1 codes (as the frontend sends) to tesseract's ISO
// 639-2/T codes.
var ocrLangMap = map[string]string{
	"en": "eng", "hi": "hin",
}

// ocrAllowedLanguages is the whitelist of tesseract language codes OCR will pass
// to `tesseract -l`. Anything outside it (an unmapped or crafted option value) is
// coerced to the default so an attacker-influenced language can never reach the
// tesseract invocation verbatim (finding I1 — docs/developer/architecture/backend-hardening.md section 3.20).
// Product scope: English and Hindi only (plus the eng+hin pair); the image
// installs exactly these two packs. Extend both together.
var ocrAllowedLanguages = map[string]bool{
	"eng": true, "hin": true,
}

// sanitizeOCRLanguage maps a requested language to a whitelisted tesseract
// code, falling back to "eng" for anything unrecognized or empty. The only
// multi-language form accepted is English + Hindi ("eng+hin" / "hin+eng",
// ISO aliases allowed) — the product's one supported pairing; any other
// combination falls back to "eng". This is the trust boundary for the
// user-supplied "language" option: the output is REBUILT from allowlisted
// codes, so no input byte reaches `tesseract -l` verbatim.
// NOTE: mirrors job-service validLanguageList (duplicated by design).
func sanitizeOCRLanguage(requested string) string {
	norm := func(part string) string {
		part = strings.TrimSpace(part)
		if mapped, ok := ocrLangMap[part]; ok {
			return mapped
		}
		return part
	}
	parts := strings.Split(strings.TrimSpace(requested), "+")
	if len(parts) == 2 {
		a, b := norm(parts[0]), norm(parts[1])
		if (a == "eng" && b == "hin") || (a == "hin" && b == "eng") {
			return a + "+" + b
		}
		return "eng"
	}
	if len(parts) != 1 {
		return "eng"
	}
	if code := norm(parts[0]); ocrAllowedLanguages[code] {
		return code
	}
	return "eng"
}

// resolveLanguage picks a tesseract language that is actually installed. The
// requested code is already mapped to tesseract's ISO 639-2 form (e.g. "eng").
// If it is available it is used as-is; otherwise it falls back to "eng", then to
// the first available language, and finally to the requested value so tesseract
// can surface its own error if nothing is installed.
//
// For a multi-language request each part is checked: installed parts are
// kept (in order), missing ones dropped; only when none is installed does
// the single-language fallback apply.
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
	var kept []string
	for _, part := range strings.Split(requested, "+") {
		if has(part) {
			kept = append(kept, part)
		}
	}
	if len(kept) > 0 {
		return strings.Join(kept, "+")
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
	out, err := procexec.Command(ctx, "tesseract", "--list-langs").CombinedOutput()
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

// ocrPDF adds a searchable text layer to a scanned PDF: rasterize each page with
// pdftoppm, OCR it with tesseract, then reassemble.
//
// Both binaries are probed up front so a missing dependency fails immediately
// with an actionable message rather than part-way through a long job. Pages are
// processed by a bounded worker pool (ocrMaxWorkers, default min(NumCPU,4))
// because each concurrent tesseract holds a full rasterized page in memory —
// unbounded parallelism is what makes OCR OOM on small hosts. Render resolution
// is separately capped by ocrMaxImageDim so a large page at high DPI cannot
// produce an image tesseract refuses.
//
// The language option is untrusted and passes through sanitizeOCRLanguage before
// it can reach a tesseract command line (finding I1 —
// docs/developer/architecture/backend-hardening.md section 3.20).
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

	// Count pages with poppler (the library that renders them) rather than
	// pdfcpu, so OCR accepts exactly the PDFs pdftoppm can render.
	pageCount, err := pdfPageCount(ctx, inputPath)
	if err != nil {
		slog.ErrorContext(ctx, "could not count PDF pages for OCR", "error", err, "op", "pdfinfo.page_count")
		return nil, fmt.Errorf("pdftoppm conversion failed: %w", err)
	}
	slog.Info("OCR rendering page by page", "dpi", dpi, "pages", pageCount, "input", inputPath)

	// Overlay mode (doc 11 §5.2): tesseract writes a text-only PDF per page and
	// the invisible text is stamped onto the ORIGINAL page, so the scan keeps
	// its quality. The old pipeline rebuilt every page from the PNG it OCRed,
	// which the first engine benchmark measured as SSIM 0.86 against the input
	// (2026-11-16). Overlay needs pdfcpu to read the input; damaged files that
	// only Poppler accepts keep the rebuild path.
	overlay := ocrOverlayPossible(ctx, inputPath, pageCount)

	// Render, OCR and delete one page at a time inside the worker pool. The
	// previous pipeline rendered every page up front, so the job's tmpfs held
	// all page images before recognition began and long scans exhausted it
	// (measured 2026-11-08: 60 pages → 60 images on disk with 2 workers). Now at
	// most `workers` page images exist at once. Defaults to a CPU-sized pool
	// capped at 4 to bound tesseract memory on small hosts; OCR_MAX_WORKERS
	// raises (or lowers) that cap on larger boxes.
	workers := ocrMaxWorkers()

	// Per page: the PDF tesseract produced (text-only in overlay mode, image +
	// text otherwise). In overlay mode a page whose OCR failed has "" and keeps
	// its original content without a text layer.
	pdfFiles := make([]string, pageCount)
	// Per-page plain-text sidecars (tesseract's `txt` output config). Indexed
	// by page so the concatenated text follows merged-PDF page order, not
	// goroutine completion order; a fallback page leaves its slot empty.
	txtFiles := make([]string, pageCount)
	var mu sync.Mutex // guards slog calls for cleaner output
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(workers)

	for i := 0; i < pageCount; i++ {
		i := i
		g.Go(func() error {
			// One cpu token per page while pdftoppm+tesseract run: across all
			// OCR jobs in this worker, at most CPU_JOB_SLOTS pages are recognised
			// at once (two jobs × OCR_MAX_WORKERS used to start up to eight
			// tesseracts on two CPUs). No slots in ctx (tests, local runs): the
			// per-job pool alone bounds it, as before.
			if slots := jobslots.FromContext(gctx); slots != nil {
				release, err := slots.Acquire(gctx, jobslots.CPU, 1)
				if err != nil {
					return err
				}
				defer release()
			}
			mu.Lock()
			slog.Info("processing OCR page", "page", i+1, "total", pageCount)
			mu.Unlock()

			pageBase := filepath.Join(tempDir, fmt.Sprintf("page-%05d", i+1))
			imgPath := pageBase + ".png"
			// Deleted however the page ends: success, fallback or error.
			defer os.Remove(imgPath)
			render := procexec.Command(gctx, "pdftoppm", ocrRenderArgs(dpi, i+1, inputPath, pageBase)...)
			if err := render.Run(); err != nil {
				slog.ErrorContext(gctx, "pdftoppm ocr-render failed", "error", err, "op", "pdftoppm.ocr_render", "page", i+1)
				return fmt.Errorf("pdftoppm conversion failed on page %d: %w", i+1, err)
			}

			outputBase := filepath.Join(tempDir, fmt.Sprintf("ocr_%05d", i+1))
			cmd := procexec.Command(gctx, "tesseract", ocrTesseractArgs(imgPath, outputBase, language, dpi, overlay)...)
			if err := cmd.Run(); err != nil {
				slog.Warn("OCR failed for page, falling back", "page", i+1, "error", err)
				if overlay {
					return nil // original page kept, no text layer
				}
				pdfFile := outputBase + ".pdf"
				if err := api.ImportImagesFile(ctx, []string{imgPath}, pdfFile, nil, nil); err != nil {
					return fmt.Errorf("failed to process page %d: %w", i+1, err)
				}
				pdfFiles[i] = pdfFile
			} else {
				pdfFiles[i] = outputBase + ".pdf"
				txtFiles[i] = outputBase + ".txt"
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	if overlay {
		if err := stampTextLayers(ctx, inputPath, outputPath, pdfFiles); err != nil {
			return nil, err
		}
	} else if len(pdfFiles) == 1 {
		if err := copyFile(pdfFiles[0], outputPath); err != nil {
			return nil, err
		}
	} else {
		if err := api.MergeCreateFile(ctx, pdfFiles, outputPath, false, nil); err != nil {
			return nil, fmt.Errorf("failed to merge OCR pages: %w", err)
		}
	}

	metadata := map[string]interface{}{
		"language":   language,
		"dpi":        dpi,
		"pagesOCRed": pageCount,
		"ocrMode":    map[bool]string{true: "overlay", false: "rebuild"}[overlay],
	}
	// The extracted text rides back on the job's metadata jsonb — a job has
	// exactly one output file, and the searchable PDF claims it. The cap keeps
	// the jsonb (and every GET /:tool/:id response) bounded; job-service scrubs
	// this key from LIST responses.
	if text, truncated := collectOCRText(txtFiles, ocrTextCapBytes); text != "" {
		metadata["extractedText"] = text
		if truncated {
			metadata["textTruncated"] = true
		}
	}

	slog.Info("OCR complete", "pagesProcessed", pageCount, "language", language)
	return metadata, nil
}

// ocrTextCapBytes bounds the extracted-text sidecar stored in job metadata.
// 512 KB of UTF-8 is ~100+ dense pages — beyond it the text is truncated and
// flagged, never dropped.
const ocrTextCapBytes = 512 * 1024

// collectOCRText concatenates per-page tesseract .txt sidecars in page order,
// form-feed-separated so page boundaries survive. Empty/missing entries (OCR
// fallback pages) contribute an empty page, keeping positions aligned with the
// merged PDF. Returns "" when no page produced any text.
// NOTE: duplicated in organize-pdf/processing/processing_scan.go by design
// (§1 no-shared-code) — keep the two in sync.
func collectOCRText(txtPaths []string, capBytes int) (string, bool) {
	pages := make([]string, len(txtPaths))
	hasText := false
	for i, p := range txtPaths {
		if p == "" {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			slog.Warn("could not read OCR text sidecar", "path", p, "error", err)
			continue
		}
		pages[i] = strings.TrimRight(string(data), " \t\n\f")
		if pages[i] != "" {
			hasText = true
		}
	}
	if !hasText {
		return "", false
	}
	text := strings.Join(pages, "\f")
	if len(text) <= capBytes {
		return text, false
	}
	cut := capBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut], true
}

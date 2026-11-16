package processing

// Output guards for the Ghostscript rewrites (compress, repair, flatten).
//
// Ghostscript's pdfwrite re-encodes every page, and the first engine benchmark
// (docs/developer/architecture/next-generation-document-engine/
// 16-benchmarking-and-testing.md, status note 2026-11-16) found two silent
// failures of that rewrite:
//
//   - text: on Devanagari (Hindi) PDFs the output renders correctly but its
//     text no longer extracts (every word came out as "फा"), so search and
//     copy/paste break. No pdfwrite option avoids it (spike 2026-11-16: no
//     subsetting, uncompressed fonts, the old interpreter, /prepress — all the
//     same), so the output is checked and a safe alternative used instead;
//   - pages: on two kinds of damaged file (a deeply nested catalog, a page tree
//     that lists itself) Ghostscript wrote 1 of 2 pages that Poppler recovers.
//
// The checks use Poppler (pdfinfo, pdftotext, pdfseparate, pdfunite), already in
// the image for OCR. Every outcome is logged and counted in
// pdf_output_guard_total.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"fyredocs/shared/metrics"
	"fyredocs/shared/procexec"
	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// ErrPagesLost reports that a rewrite produced fewer pages than the input has,
// even after rebuilding the input's page tree. The job fails rather than hand
// the user a silently shorter document.
var ErrPagesLost = errors.New("output has fewer pages than the input")

const (
	// textGuardPages is how many leading pages the text guard compares: enough
	// to catch a font-level breakage (it hits every page alike), cheap enough to
	// run on every job.
	textGuardPages = 3
	// textGuardMinRunes: inputs with less extractable text than this (scans,
	// image-only pages) have nothing to protect, and the check is skipped.
	textGuardMinRunes = 20
	// textGuardMaxRunes caps each side before the edit distance, which is
	// quadratic.
	textGuardMaxRunes = 4000
	// textGuardMaxCER is the largest character error rate still accepted. A
	// healthy Latin rewrite measures 0 (benchmark 2026-11-16, 446 jobs); the
	// Devanagari breakage measured 0.86.
	textGuardMaxCER = 0.05
)

// rewrite describes one Ghostscript rewrite and how to recover when its output
// would lose text.
type rewrite struct {
	tool string // tool type, for logs and the metric
	// args returns the Ghostscript arguments that rewrite in into the output.
	args func(in string) []string
	// textFallback, if set, writes a text-preserving alternative of in to out.
	// When it is nil (or fails), the Ghostscript output is kept with a warning.
	textFallback func(ctx context.Context, in, out string) error
}

// runGuardedRewrite runs a Ghostscript rewrite of inputPath into outputPath and
// then enforces the page and text guards. The returned metadata may carry
// "pageTreeRebuilt", "fallback", "textExtractable" and "warnings".
func runGuardedRewrite(ctx context.Context, rw rewrite, inputPath, outputPath string) (map[string]interface{}, error) {
	meta := map[string]interface{}{}
	if err := runGhostscript(ctx, rw.tool, rw.args(inputPath)); err != nil {
		return nil, err
	}

	src, cleanup, err := guardPages(ctx, rw, inputPath, outputPath, meta)
	defer cleanup()
	if err != nil {
		return nil, err
	}
	guardText(ctx, rw, src, outputPath, meta)
	return meta, nil
}

// guardPages compares page counts. If the output is short it rebuilds the
// input's page tree with Poppler (pdfseparate + pdfunite) and rewrites again.
// It returns the input the output was finally made from.
func guardPages(ctx context.Context, rw rewrite, inputPath, outputPath string, meta map[string]interface{}) (string, func(), error) {
	noop := func() {}
	inPages, errIn := pdfPageCount(ctx, inputPath)
	outPages, errOut := pdfPageCount(ctx, outputPath)
	if errIn != nil || errOut != nil {
		// Nothing to compare (Poppler cannot count the input, or the output is
		// unreadable, which the caller's own checks report).
		countGuard(rw.tool, "pages", "skipped")
		return inputPath, noop, nil
	}
	if outPages >= inPages {
		countGuard(rw.tool, "pages", "ok")
		return inputPath, noop, nil
	}

	slog.WarnContext(ctx, "rewrite lost pages, rebuilding page tree", "tool", rw.tool, "inputPages", inPages, "outputPages", outPages)
	dir, err := os.MkdirTemp(filepath.Dir(outputPath), "pages-")
	if err != nil {
		return "", noop, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	rebuilt, err := rebuildPageTree(ctx, inputPath, dir)
	if err == nil {
		err = runGhostscript(ctx, rw.tool, rw.args(rebuilt))
	}
	if err == nil {
		outPages, err = pdfPageCount(ctx, outputPath)
	}
	if err != nil || outPages < inPages {
		countGuard(rw.tool, "pages", "failed")
		slog.ErrorContext(ctx, "rewrite still loses pages", "tool", rw.tool, "inputPages", inPages, "outputPages", outPages, "error", err)
		return "", cleanup, fmt.Errorf("%s: %w (input %d pages, output %d)", rw.tool, ErrPagesLost, inPages, outPages)
	}
	countGuard(rw.tool, "pages", "retried")
	meta["pageTreeRebuilt"] = true
	return rebuilt, cleanup, nil
}

// rebuildPageTree writes every page Poppler can see in in as its own file and
// joins them again, which yields a plain, flat page tree.
func rebuildPageTree(ctx context.Context, in, dir string) (string, error) {
	pattern := filepath.Join(dir, "page-%d.pdf")
	if out, err := procexec.Command(ctx, "pdfseparate", in, pattern).CombinedOutput(); err != nil {
		return "", fmt.Errorf("pdfseparate: %w: %s", err, firstLine(out))
	}
	pages, err := filepath.Glob(filepath.Join(dir, "page-*.pdf"))
	if err != nil || len(pages) == 0 {
		return "", fmt.Errorf("pdfseparate produced no pages")
	}
	sortPageFiles(pages)
	rebuilt := filepath.Join(dir, "rebuilt.pdf")
	args := append(pages, rebuilt)
	if out, err := procexec.Command(ctx, "pdfunite", args...).CombinedOutput(); err != nil {
		return "", fmt.Errorf("pdfunite: %w: %s", err, firstLine(out))
	}
	return rebuilt, nil
}

// sortPageFiles orders page-N.pdf by N (a string sort puts page-10 before page-2).
func sortPageFiles(files []string) {
	num := func(p string) int {
		var n int
		fmt.Sscanf(strings.TrimSuffix(filepath.Base(p), ".pdf"), "page-%d", &n)
		return n
	}
	sort.Slice(files, func(i, j int) bool { return num(files[i]) < num(files[j]) })
}

// guardText checks the output's text against src. On a mismatch it writes the
// rewrite's text fallback over the output, or, without one, keeps the output
// and records a warning in meta.
func guardText(ctx context.Context, rw rewrite, src, outputPath string, meta map[string]interface{}) {
	want, err := pdfText(ctx, src, textGuardPages)
	if err != nil || len([]rune(want)) < textGuardMinRunes {
		countGuard(rw.tool, "text", "skipped")
		return
	}
	got, err := pdfText(ctx, outputPath, textGuardPages)
	cer := 1.0
	if err == nil {
		cer = charErrorRate(want, got)
	}
	if cer <= textGuardMaxCER {
		countGuard(rw.tool, "text", "ok")
		return
	}

	slog.WarnContext(ctx, "rewrite broke text extraction", "tool", rw.tool, "cer", cer)
	if rw.textFallback != nil {
		ferr := rw.textFallback(ctx, src, outputPath)
		if ferr == nil {
			countGuard(rw.tool, "text", "fallback")
			meta["fallback"] = "text-preserving"
			addWarning(meta, warnTextKeptLightCompression)
			return
		}
		slog.WarnContext(ctx, "text-preserving fallback failed, keeping rewrite", "tool", rw.tool, "error", ferr)
	}
	countGuard(rw.tool, "text", "warned")
	meta["textExtractable"] = false
	addWarning(meta, warnTextNotSearchable)
}

// optimizeLossless is the text-preserving fallback for compress and repair:
// pdfcpu's optimiser keeps fonts and text untouched and only drops duplicate
// and unused objects, so the saving is smaller than Ghostscript's.
func optimizeLossless(ctx context.Context, in, out string) error {
	tmp := out + ".pdfcpu"
	if err := api.OptimizeFile(ctx, in, tmp, nil, nil); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, out)
}

// pdfText extracts the first pages of path as UTF-8 with whitespace collapsed.
func pdfText(ctx context.Context, path string, pages int) (string, error) {
	out, err := procexec.Command(ctx, "pdftotext", "-enc", "UTF-8", "-f", "1", "-l", fmt.Sprint(pages), path, "-").Output()
	if err != nil {
		return "", err
	}
	return strings.Join(strings.Fields(string(out)), " "), nil
}

// charErrorRate is the edit distance between want and got over len(want), on
// runes, each side capped at textGuardMaxRunes.
func charErrorRate(want, got string) float64 {
	a, b := []rune(want), []rune(got)
	if len(a) > textGuardMaxRunes {
		a = a[:textGuardMaxRunes]
	}
	if len(b) > textGuardMaxRunes {
		b = b[:textGuardMaxRunes]
	}
	if len(a) == 0 {
		if len(b) == 0 {
			return 0
		}
		return 1
	}
	prev, cur := make([]int, len(b)+1), make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return float64(prev[len(b)]) / float64(len(a))
}

// runGhostscript runs gs with args, logging stderr (never returned: it names
// internal paths and versions).
func runGhostscript(ctx context.Context, tool string, args []string) error {
	gsPath, err := findGhostscript()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd := procexec.Command(ctx, gsPath, args...)
	cmd.Stderr = &stderr
	cmd.Stdout = io.Discard
	if err := cmd.Run(); err != nil {
		slog.ErrorContext(ctx, "ghostscript failed", "error", err, "op", "ghostscript."+tool, "stderr", stderr.String())
		return fmt.Errorf("ghostscript %s failed: %w", tool, err)
	}
	return nil
}

// JobWarning is one entry of the job's metadata.warnings: the same
// {page, code} shape organize-pdf's scan-to-pdf uses (ScanWarning), with page
// omitted for document-wide warnings. The web app maps codes to sentences
// (fyredocs_frontend src/lib/jobWarnings.ts); unknown codes are not shown.
type JobWarning struct {
	Page int    `json:"page,omitempty"`
	Code string `json:"code"`
}

const (
	// warnTextNotSearchable: the output renders correctly but its text no
	// longer extracts (flatten, or a fallback that failed).
	warnTextNotSearchable = "text-not-searchable"
	// warnTextKeptLightCompression: Ghostscript would have broken the text, so
	// the lossless pdfcpu optimiser was used instead (smaller saving).
	warnTextKeptLightCompression = "text-kept-light-compression"
)

func addWarning(meta map[string]interface{}, code string) {
	ws, _ := meta["warnings"].([]JobWarning)
	meta["warnings"] = append(ws, JobWarning{Code: code})
}

func countGuard(tool, guard, outcome string) {
	metrics.OutputGuard.WithLabelValues("optimize-pdf", tool, guard, outcome).Inc()
}

func firstLine(b []byte) string {
	s, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	return s
}

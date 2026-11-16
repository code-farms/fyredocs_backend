package processing

// Text guard for the Ghostscript PDF/A rewrite.
//
// Ghostscript's pdfwrite breaks text extraction on Devanagari (Hindi) PDFs: the
// output renders correctly, but every word extracts as "फा", so search and
// copy/paste stop working (first engine benchmark, 2026-11-16, doc 16 status
// note). No pdfwrite option avoids it. PDF/A has no lossless alternative (the
// conversion IS the rewrite), so decided 2026-11-16: keep the PDF/A file and say
// so in the job metadata (textExtractable=false plus a warning).
//
// NOTE: pdfText, charErrorRate and the constants are duplicated from
// optimize-pdf/processing/pdfguard.go; CLAUDE.md §1 forbids cross-service
// imports. Keep the thresholds in step.

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"fyredocs/shared/metrics"
	"fyredocs/shared/procexec"
)

const (
	textGuardPages    = 3
	textGuardMinRunes = 20
	textGuardMaxRunes = 4000
	textGuardMaxCER   = 0.05
)

// checkTextKept compares the first pages' text of in and out and, when the
// rewrite broke it, records textExtractable=false and a warning in meta.
func checkTextKept(ctx context.Context, tool, in, out string, meta map[string]interface{}) {
	want, err := pdfText(ctx, in, textGuardPages)
	if err != nil || len([]rune(want)) < textGuardMinRunes {
		countTextGuard(tool, "skipped")
		return
	}
	got, err := pdfText(ctx, out, textGuardPages)
	cer := 1.0
	if err == nil {
		cer = charErrorRate(want, got)
	}
	if cer <= textGuardMaxCER {
		countTextGuard(tool, "ok")
		return
	}
	slog.WarnContext(ctx, "rewrite broke text extraction", "tool", tool, "cer", cer)
	countTextGuard(tool, "warned")
	meta["textExtractable"] = false
	ws, _ := meta["warnings"].([]JobWarning)
	meta["warnings"] = append(ws, JobWarning{Code: "text-not-searchable"})
}

// JobWarning is one entry of the job's metadata.warnings, the {page, code}
// shape the web app reads (page omitted: document-wide).
// NOTE: same type as optimize-pdf/processing/pdfguard.go JobWarning.
type JobWarning struct {
	Page int    `json:"page,omitempty"`
	Code string `json:"code"`
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

func countTextGuard(tool, outcome string) {
	metrics.OutputGuard.WithLabelValues("convert-from-pdf", tool, "text", outcome).Inc()
}

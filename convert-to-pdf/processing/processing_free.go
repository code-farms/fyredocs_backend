package processing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyredocs/shared/metrics"
	"fyredocs/shared/procexec"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// envOrDefault reads an environment variable with a fallback, treating empty as
// unset so a variable exported as "" behaves like an absent one.
func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// daemonDialTimeout bounds the pre-flight dial to a pooled daemon in
// tryUnoconvert. The daemon is on the same host (loopback), so a live one
// answers in microseconds; 200 ms only matters when the port is filtered.
const daemonDialTimeout = 200 * time.Millisecond

// Size-scaled unoconvert deadline: the documented LibreOffice cost model is
// ~2 s + ~1.5 s per MB of input (docs/developer/architecture/
// production-readiness.md). The old fixed 30 s deadline therefore failed every
// warm conversion above roughly 18 MB, sending large files to a cold
// LibreOffice that then had to redo the work from scratch.
const (
	// unoconvertMinTimeout is the floor, so small files keep the old budget.
	unoconvertMinTimeout = 30 * time.Second
	// unoconvertBaseCost is the fixed per-conversion cost in the model.
	unoconvertBaseCost = 2 * time.Second
	// unoconvertPerMB is the per-megabyte cost in the model.
	unoconvertPerMB = 1500 * time.Millisecond
)

// unoconvertTimeout returns max(unoconvertMinTimeout, base + perMB × MB) for an
// input of inputBytes. It is not capped here: context.WithTimeout on the job
// context keeps the job deadline (10 minutes, internal/worker) as the ceiling.
// A non-positive size (unknown) gets the floor.
func unoconvertTimeout(inputBytes int64) time.Duration {
	if inputBytes <= 0 {
		return unoconvertMinTimeout
	}
	scaled := unoconvertBaseCost + time.Duration(float64(unoconvertPerMB)*float64(inputBytes)/(1<<20))
	if scaled < unoconvertMinTimeout {
		return unoconvertMinTimeout
	}
	return scaled
}

// tryUnoconvert attempts a conversion through a pooled unoserver daemon. It
// blocks until a daemon port is free (bounding concurrency to the pool size),
// runs unoconvert against it, then releases the port BEFORE returning so the
// slow LibreOffice fallback never holds a daemon slot. Returns true on success;
// false (output already logged) means the caller should fall back.
func tryUnoconvert(ctx context.Context, inputPath, outputPath, convertTo string) bool {
	port, ok := pool.acquire(ctx)
	if !ok {
		return false
	}
	// The port goes back to the pool when this returns, unless a recycle takes
	// it over: recycleThenRelease releases it once the fresh daemon answers.
	releaseOnReturn := true
	defer func() {
		if releaseOnReturn {
			pool.release(port)
		}
	}()

	// Check the daemon is listening before handing the job to unoconvert. The
	// unoconvert client does not fail fast on a dead port: it retries the
	// connection 5 times with a 10 s sleep (unoserver/client.py _connect, no CLI
	// flag), so without this check every job on a dead pool burned the full 30 s
	// deadline below before even starting the cold fallback — measured
	// 2026-11-08 in the test-binaries image. A local dial costs well under a
	// millisecond when the daemon is up.
	if DaemonsUp(pool.host, []string{port}, daemonDialTimeout) == 0 {
		metrics.ConvertFallback.WithLabelValues("convert-to-pdf", "no_daemon").Inc()
		slog.Warn("unoserver daemon not listening, falling back to direct libreoffice",
			"port", port, "convertTo", convertTo, "reason", "no_daemon")
		return false
	}

	var inputBytes int64
	if info, err := os.Stat(inputPath); err == nil {
		inputBytes = info.Size()
	}
	unoCtx, unoCancel := context.WithTimeout(ctx, unoconvertTimeout(inputBytes))
	defer unoCancel()
	cmd := procexec.Command(unoCtx, "unoconvert",
		"--host", pool.host, "--port", port,
		"--convert-to", convertTo,
		inputPath, outputPath)
	output, err := cmd.CombinedOutput()
	if err == nil {
		uses := pool.recordUse(port)
		if cause := recycleCause(uses, maxConversions(), func() int64 { return daemonRSS(port) }, maxRSSMB()); cause != "" {
			releaseOnReturn = false
			go recycleThenRelease(port, cause)
		}
		return true
	}
	if unoCtx.Err() != nil {
		// The client was killed, but the daemon is still converting the file
		// and would make the next job on this port queue behind work nobody
		// wants. Restart it and keep the port out of the pool until the fresh
		// daemon answers (recycleThenRelease).
		releaseOnReturn = false
		go recycleThenRelease(port, "timeout")
	}
	if ctx.Err() != nil {
		// The job itself was cancelled or ran out of time: there is nothing to
		// fall back for, and counting it would make the fallback metric
		// report cancellations as pool problems.
		return false
	}
	reason := fallbackReason(unoCtx.Err(), string(output))
	metrics.ConvertFallback.WithLabelValues("convert-to-pdf", reason).Inc()
	slog.Warn("unoconvert failed, falling back to direct libreoffice",
		"port", port, "convertTo", convertTo, "reason", reason, "output", string(output), "error", err)
	return false
}

// fallbackReason maps a failed unoconvert call to the closed label set of
// convert_fallback_total. "no_daemon" means nothing listened on the pooled port
// (the pool is broken or a daemon died); "timeout" means the per-call deadline
// fired; anything else is "daemon_error". Keep this set closed — the value is a
// Prometheus label, so free text here would explode cardinality.
func fallbackReason(ctxErr error, output string) string {
	switch {
	case errors.Is(ctxErr, context.DeadlineExceeded):
		return "timeout"
	case strings.Contains(output, "Connection refused"):
		return "no_daemon"
	default:
		return "daemon_error"
	}
}

// officeToPDF converts an office document to PDF, preferring a warm unoserver
// daemon from the pool and falling back to spawning LibreOffice directly.
//
// The fallback is correct but slow — a cold LibreOffice start costs seconds — so it
// is meant to be effectively unreachable in a healthy container. If it starts
// happening routinely, the daemon pool is misconfigured; see unopool.go.
func officeToPDF(ctx context.Context, inputPath string, outputPath string, fileType string) error {
	slog.Info("converting to PDF", "type", fileType, "input", inputPath)

	// Fast path: unoconvert via a pooled persistent LibreOffice daemon.
	if tryUnoconvert(ctx, inputPath, outputPath, "pdf") {
		return nil
	}

	// Slow fallback: spawn a fresh LibreOffice process with its own hardened
	// profile (see newFallbackProfile), next to the output in the job scratch dir.
	outputDir := filepath.Dir(outputPath)
	profileArg, cleanup, err := newFallbackProfile(filepath.Dir(outputDir))
	if err != nil {
		return fmt.Errorf("conversion failed: %w", err)
	}
	defer cleanup()
	cmd := procexec.Command(ctx, "libreoffice", profileArg, "--headless", "--convert-to", "pdf", "--outdir", outputDir, inputPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.ErrorContext(ctx, "libreoffice convert failed", "error", err, "op", "libreoffice.convert", "type", fileType, "output", string(output))
		return fmt.Errorf("conversion failed: %w", err)
	}

	convertedFile := filepath.Join(outputDir, strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))+".pdf")
	if convertedFile != outputPath {
		return os.Rename(convertedFile, outputPath)
	}
	return nil
}

// officeToOffice converts between office formats (for example .docx to .odt)
// without going through PDF, using the same daemon pool as officeToPDF.
func officeToOffice(ctx context.Context, inputPath string, outputPath string, outputFormat string) error {
	slog.Info("converting office to ODF", "format", outputFormat, "input", inputPath)

	// Fast path: unoconvert via a pooled persistent LibreOffice daemon.
	if tryUnoconvert(ctx, inputPath, outputPath, outputFormat) {
		return nil
	}

	// Slow fallback: spawn a fresh LibreOffice process with its own hardened
	// profile (see newFallbackProfile).
	outputDir := filepath.Dir(outputPath)
	profileArg, cleanup, err := newFallbackProfile(filepath.Dir(outputDir))
	if err != nil {
		return fmt.Errorf("conversion to %s failed: %w", outputFormat, err)
	}
	defer cleanup()
	cmd := procexec.Command(ctx, "libreoffice", profileArg, "--headless", "--convert-to", outputFormat, "--outdir", outputDir, inputPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.ErrorContext(ctx, "libreoffice convert failed", "error", err, "op", "libreoffice.convert", "format", outputFormat, "output", string(output))
		return fmt.Errorf("conversion to %s failed: %w", outputFormat, err)
	}

	convertedFile := filepath.Join(outputDir, strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))+"."+outputFormat)
	if convertedFile != outputPath {
		return os.Rename(convertedFile, outputPath)
	}
	return nil
}

// imageToPDF wraps one or more images into a PDF, page per image. Does not go
// through LibreOffice — the images are placed directly, which is both faster and
// avoids LibreOffice's own rescaling.
func imageToPDF(ctx context.Context, inputPaths []string, outputPath string) error {
	slog.Info("converting images to PDF", "count", len(inputPaths))
	return api.ImportImagesFile(ctx, inputPaths, outputPath, nil, nil)
}

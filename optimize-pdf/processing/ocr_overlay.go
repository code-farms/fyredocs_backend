package processing

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// ocrStampDesc places a text-only page over the original page: centred,
// scaled 1:1 (tesseract wrote it at the render DPI, so it has the page's size in
// points), unrotated. The text is invisible (render mode 3), so the page looks
// exactly as before.
const ocrStampDesc = "position:c, scalefactor:1 abs, rotation:0"

// ocrOverlayPossible reports whether the text layer can be stamped onto the
// input: pdfcpu must read it and count the same pages Poppler rendered.
// Damaged files only Poppler accepts fall back to rebuilding pages from images.
func ocrOverlayPossible(ctx context.Context, inputPath string, popplerPages int) bool {
	n, err := api.PageCountFile(ctx, inputPath)
	if err != nil || n != popplerPages {
		slog.WarnContext(ctx, "OCR overlay unavailable, rebuilding pages from images", "error", err, "pdfcpuPages", n, "popplerPages", popplerPages)
		return false
	}
	return true
}

// ocrTesseractArgs returns tesseract's arguments for one page image. In
// overlay mode it writes a text-only PDF (textonly_pdf=1: no image inside, so
// the per-page PDFs stay small and the scan is not re-encoded); --dpi makes the
// PDF page the size of the original page in points.
func ocrTesseractArgs(imgPath, outputBase, language string, dpi int, overlay bool) []string {
	args := []string{imgPath, outputBase, "-l", language}
	if overlay {
		args = append(args, "--dpi", strconv.Itoa(dpi), "-c", "textonly_pdf=1")
	}
	return append(args, "pdf", "txt")
}

// stampTextLayers stamps textPDFs[i] (one page each, "" for none) onto page i+1
// of inputPath and writes outputPath. The text-only pages are a few KB each, so
// they are read into memory rather than held open.
func stampTextLayers(ctx context.Context, inputPath, outputPath string, textPDFs []string) error {
	stamps := map[int]*model.Watermark{}
	for i, f := range textPDFs {
		if f == "" {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("read OCR text layer for page %d: %w", i+1, err)
		}
		wm, err := api.PDFWatermarkForReadSeeker(ctx, bytes.NewReader(data), 1, ocrStampDesc, true, false, types.POINTS)
		if err != nil {
			return fmt.Errorf("prepare OCR text layer for page %d: %w", i+1, err)
		}
		stamps[i+1] = wm
	}
	if len(stamps) == 0 {
		slog.WarnContext(ctx, "OCR produced no text layer on any page; returning the input unchanged")
		return copyFile(inputPath, outputPath)
	}
	if err := api.AddWatermarksMapFile(ctx, inputPath, outputPath, stamps, nil); err != nil {
		return fmt.Errorf("stamp OCR text layers: %w", err)
	}
	return nil
}

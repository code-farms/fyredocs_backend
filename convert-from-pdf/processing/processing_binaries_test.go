//go:build integration && binaries

// Real-binary exec-path tests for the convert-from-pdf worker: pdftoppm
// (pdf-to-image), pdftotext, and pdftohtml. Run via `make test-binaries`
// (dockerized — see deployment/test-binaries.Dockerfile). PDF fixtures are
// generated in-code: image PDFs via pdfcpu, and the text-layer PDF via
// LibreOffice from a .txt source, because pdfcpu offers no simple way to
// embed an extractable text layer. The pdf2docx / pdf_to_xlsx.py Python
// paths are NOT covered — they need this service's debian/pip runtime image
// (see the descope note in deployment/test-binaries.Dockerfile).
package processing

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// TestMain mirrors main.go's DisableConfigDir: without it, the first pdfcpu
// call from the test runner loads — and on first use writes — a config.yml
// under os.UserConfigDir(), making behavior depend on the container's HOME.
func TestMain(m *testing.M) {
	api.DisableConfigDir()
	os.Exit(m.Run())
}

// writeGradientPNG writes a small gradient PNG — real varying raster content
// for pdftoppm to render, without needing any font machinery.
func writeGradientPNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), 120, 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// buildImagePDF assembles an n-page image PDF via pdfcpu — the established
// in-code fixture recipe.
func buildImagePDF(t *testing.T, dir string, pages int) string {
	t.Helper()
	imgs := make([]string, pages)
	for i := range imgs {
		p := filepath.Join(dir, "page"+string(rune('a'+i))+".png")
		writeGradientPNG(t, p, 200, 260)
		imgs[i] = p
	}
	input := filepath.Join(dir, "input.pdf")
	if err := api.ImportImagesFile(imgs, input, nil, nil); err != nil {
		t.Fatalf("build input pdf: %v", err)
	}
	return input
}

// buildTextPDF produces a PDF with a real extractable text layer by writing a
// .txt and converting it with LibreOffice (available in the binaries image).
// pdfcpu imports images, not text, so this is the only in-code route to a
// text-layer fixture.
func buildTextPDF(t *testing.T, dir, text string) string {
	t.Helper()
	src := filepath.Join(dir, "source.txt")
	if err := os.WriteFile(src, []byte(text+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(context.Background(), "libreoffice",
		"--headless", "--convert-to", "pdf", "--outdir", dir, src).CombinedOutput()
	if err != nil {
		t.Fatalf("libreoffice fixture conversion failed: %v (output: %s)", err, out)
	}
	pdf := filepath.Join(dir, "source.pdf")
	if info, err := os.Stat(pdf); err != nil || info.Size() == 0 {
		t.Fatalf("libreoffice produced no fixture PDF: %v (output: %s)", err, out)
	}
	return pdf
}

// pngMagic is the 8-byte PNG signature.
var pngMagic = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}

// TestPdfToImagesBinaries drives the real pdftoppm path in both output
// shapes: a single-page PDF returns the bare image, a multi-page PDF returns
// a ZIP of page images. Every image must decode, not merely exist.
func TestPdfToImagesBinaries(t *testing.T) {
	t.Run("single page returns bare image", func(t *testing.T) {
		dir := t.TempDir()
		input := buildImagePDF(t, dir, 1)
		outDir := t.TempDir()

		outPath, err := pdfToImages(context.Background(), input, outDir, "result", map[string]interface{}{"format": "png", "dpi": "150"})
		if err != nil {
			t.Fatalf("pdfToImages: %v", err)
		}
		if filepath.Ext(outPath) != ".png" {
			t.Fatalf("single-page output should be .png, got %s", outPath)
		}
		data, err := os.ReadFile(outPath)
		if err != nil || len(data) == 0 {
			t.Fatalf("output missing or empty: %v", err)
		}
		if !bytes.HasPrefix(data, pngMagic) {
			t.Fatal("output lacks PNG magic bytes")
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("output does not decode as PNG: %v", err)
		}
		if img.Bounds().Dx() == 0 || img.Bounds().Dy() == 0 {
			t.Error("decoded image has zero dimensions")
		}
	})

	t.Run("multi page returns zip of decodable images", func(t *testing.T) {
		dir := t.TempDir()
		input := buildImagePDF(t, dir, 3)
		outDir := t.TempDir()

		outPath, err := pdfToImages(context.Background(), input, outDir, "result", nil)
		if err != nil {
			t.Fatalf("pdfToImages: %v", err)
		}
		if filepath.Ext(outPath) != ".zip" {
			t.Fatalf("multi-page output should be .zip, got %s", outPath)
		}
		zr, err := zip.OpenReader(outPath)
		if err != nil {
			t.Fatalf("output does not open as zip: %v", err)
		}
		defer zr.Close()
		if len(zr.File) != 3 {
			t.Fatalf("zip entry count = %d, want 3", len(zr.File))
		}
		for _, zf := range zr.File {
			if !strings.HasSuffix(zf.Name, ".png") {
				t.Errorf("zip entry %q is not a .png", zf.Name)
				continue
			}
			rc, err := zf.Open()
			if err != nil {
				t.Fatalf("open zip entry %s: %v", zf.Name, err)
			}
			if _, err := png.Decode(rc); err != nil {
				t.Errorf("zip entry %s does not decode as PNG: %v", zf.Name, err)
			}
			rc.Close()
		}
	})
}

// TestPdfToTextBinaries runs real pdftotext over a LibreOffice-generated
// text-layer PDF and asserts the words come back out.
func TestPdfToTextBinaries(t *testing.T) {
	dir := t.TempDir()
	input := buildTextPDF(t, dir, "Fyredocs binaries fixture text")
	output := filepath.Join(t.TempDir(), "out.txt")

	if err := pdfToText(context.Background(), input, output); err != nil {
		t.Fatalf("pdfToText: %v", err)
	}
	data, err := os.ReadFile(output)
	if err != nil || len(data) == 0 {
		t.Fatalf("output missing or empty: %v", err)
	}
	if !strings.Contains(string(data), "Fyredocs binaries fixture text") {
		t.Errorf("extracted text missed the source words: %q", data)
	}
}

// TestPdfToHTMLBinaries runs real pdftohtml and asserts the ZIP contains an
// HTML page carrying the source text.
func TestPdfToHTMLBinaries(t *testing.T) {
	dir := t.TempDir()
	input := buildTextPDF(t, dir, "Fyredocs HTML conversion fixture")
	output := filepath.Join(t.TempDir(), "out.zip")

	if err := pdfToHTML(context.Background(), input, output); err != nil {
		t.Fatalf("pdfToHTML: %v", err)
	}
	zr, err := zip.OpenReader(output)
	if err != nil {
		t.Fatalf("output does not open as zip: %v", err)
	}
	defer zr.Close()

	foundHTML := false
	for _, zf := range zr.File {
		if !strings.HasSuffix(zf.Name, ".html") {
			continue
		}
		foundHTML = true
		rc, err := zf.Open()
		if err != nil {
			t.Fatalf("open zip entry %s: %v", zf.Name, err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read zip entry %s: %v", zf.Name, err)
		}
		if !strings.Contains(string(body), "Fyredocs HTML conversion fixture") {
			t.Errorf("HTML entry %s missed the source text", zf.Name)
		}
	}
	if !foundHTML {
		t.Error("zip contains no .html entry")
	}
}

// TestPdfConvertCorruptInputBinaries feeds garbage bytes with a .pdf name to
// each poppler exec path: every one must return an error rather than emit an
// output the pipeline would upload.
func TestPdfConvertCorruptInputBinaries(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "garbage.pdf")
	if err := os.WriteFile(input, []byte("%PDF-1.7 then it all goes wrong \x00\x01"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("pdftotext", func(t *testing.T) {
		output := filepath.Join(t.TempDir(), "out.txt")
		if err := pdfToText(context.Background(), input, output); err == nil {
			t.Fatal("expected error for corrupt input")
		}
	})

	t.Run("pdftohtml", func(t *testing.T) {
		output := filepath.Join(t.TempDir(), "out.zip")
		if err := pdfToHTML(context.Background(), input, output); err == nil {
			t.Fatal("expected error for corrupt input")
		}
	})

	t.Run("pdf-to-image", func(t *testing.T) {
		outDir := t.TempDir()
		if _, err := pdfToImages(context.Background(), input, outDir, "result", nil); err == nil {
			t.Fatal("expected error for corrupt input")
		}
		if entries, _ := os.ReadDir(outDir); len(entries) != 0 {
			t.Errorf("corrupt input left %d files in the output dir", len(entries))
		}
	})
}

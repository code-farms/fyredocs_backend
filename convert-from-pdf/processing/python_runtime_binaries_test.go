//go:build integration && binaries

package processing

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The pdf2docx and pdfplumber paths need convert-from-pdf's Debian/pip
// runtime, which the shared test-binaries image does not carry. These tests skip
// there and run inside the real convert-from-pdf image via
// `make test-convert-from-pdf-image`. They guard the Python dependency upgrades
// (pdf2docx, PyMuPDF, OpenCV, pdfplumber/pdfminer.six) that the image takes.

// requirePythonRuntime skips unless cmd works in this environment.
func requirePythonRuntime(t *testing.T, name string, args ...string) {
	t.Helper()
	if err := exec.Command(name, args...).Run(); err != nil {
		t.Skipf("%s %v unavailable (%v): run in the convert-from-pdf image with make test-convert-from-pdf-image", name, args, err)
	}
}

// zipEntry returns the contents of name inside the zip at path.
func zipEntry(t *testing.T, path, name string) string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("%s is not a zip package: %v", path, err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer rc.Close()
			b, _ := io.ReadAll(rc)
			return string(b)
		}
	}
	t.Fatalf("%s has no %s", path, name)
	return ""
}

// zipEntryIfPresent is zipEntry for an optional member: "" when absent.
func zipEntryIfPresent(t *testing.T, path, name string) string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("%s is not a zip package: %v", path, err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == name {
			return zipEntry(t, path, name)
		}
	}
	return ""
}

// TestPdfToWordBinaries: pdf2docx turns a text PDF into a DOCX that keeps the
// text in the document body.
func TestPdfToWordBinaries(t *testing.T) {
	requirePythonRuntime(t, "pdf2docx", "--help")
	input := buildTextPDF(t, t.TempDir(), "Fyredocs pdf2docx fixture sentence")
	out := filepath.Join(t.TempDir(), "out.docx")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := pdfToDocxNative(ctx, input, out); err != nil {
		t.Fatalf("pdf2docx: %v", err)
	}
	body := zipEntry(t, out, "word/document.xml")
	if !strings.Contains(body, "pdf2docx fixture") {
		t.Errorf("document.xml does not contain the fixture text (%d bytes)", len(body))
	}
}

// TestPdfToExcelBinaries: the pdfplumber/openpyxl helper turns a PDF into a
// workbook with a worksheet holding the PDF's text.
func TestPdfToExcelBinaries(t *testing.T) {
	if _, err := os.Stat(pdfToXlsxScript()); err != nil {
		t.Skipf("%s not present: run in the convert-from-pdf image with make test-convert-from-pdf-image", pdfToXlsxScript())
	}
	requirePythonRuntime(t, "python3", "-c", "import pdfplumber, openpyxl")
	input := buildTextPDF(t, t.TempDir(), "Item Quantity\nWidget 3\nGadget 7")
	out := filepath.Join(t.TempDir(), "out.xlsx")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := pdfToXlsx(ctx, input, out); err != nil {
		t.Fatalf("pdf_to_xlsx: %v", err)
	}
	if wb := zipEntry(t, out, "xl/workbook.xml"); !strings.Contains(wb, "<sheet ") {
		t.Errorf("workbook has no sheet")
	}
	// Strings may be inline in the sheet or in sharedStrings.xml, depending on
	// how the workbook was written; check both.
	sheet := zipEntry(t, out, "xl/worksheets/sheet1.xml") + zipEntryIfPresent(t, out, "xl/sharedStrings.xml")
	if !strings.Contains(sheet, "Widget") {
		t.Errorf("worksheet does not contain the PDF text")
	}
}

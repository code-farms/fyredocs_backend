//go:build integration && binaries

// Real-binary tests for the output guards (pdfguard.go): Ghostscript rewrites
// must not silently break text extraction or drop pages. Both failures were
// found by the first engine benchmark (2026-11-16) on these exact shapes.
package processing

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// hindiSentence is the benchmark's Devanagari text; Ghostscript 10.08 turns it
// into "फा फा …" on extraction.
const hindiSentence = "यह फायरडॉक्स का हिंदी परीक्षण दस्तावेज़ है। भुगतान की स्थिति, वितरण की तारीख और कुल राशि नीचे दी गई है।"

// buildOfficeTextPDF converts text to PDF with LibreOffice (in the binaries
// image), which embeds subset TrueType fonts with a ToUnicode map, the kind of
// file that breaks.
// NOTE: duplicated from convert-from-pdf/processing/processing_binaries_test.go
// buildTextPDF; CLAUDE.md §1 forbids cross-service imports.
func buildOfficeTextPDF(t *testing.T, dir, text string) string {
	t.Helper()
	src := filepath.Join(dir, "source.txt")
	if err := os.WriteFile(src, []byte(text+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	profile := "file://" + filepath.Join(dir, "lo-profile")
	out, err := exec.CommandContext(context.Background(), "libreoffice", "-env:UserInstallation="+profile,
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

func extractText(t *testing.T, path string) string {
	t.Helper()
	txt, err := pdfText(context.Background(), path, 3)
	if err != nil {
		t.Fatalf("pdftotext %s: %v", path, err)
	}
	return txt
}

func TestRewritesKeepHindiTextBinaries(t *testing.T) {
	dir := t.TempDir()
	input := buildOfficeTextPDF(t, dir, strings.Repeat(hindiSentence+"\n\n", 6))
	if !strings.Contains(extractText(t, input), "फायरडॉक्स") {
		t.Fatal("fixture: input text does not extract; the test would prove nothing")
	}

	for _, tc := range []struct {
		tool     string
		run      func(in, out string) (map[string]interface{}, error)
		fallback bool // true: text must survive; false: a warning is enough
	}{
		{"compress-pdf", func(in, out string) (map[string]interface{}, error) {
			return compressPDF(context.Background(), in, out, map[string]interface{}{"quality": "medium"})
		}, true},
		{"repair-pdf", func(in, out string) (map[string]interface{}, error) { return repairPDF(context.Background(), in, out) }, true},
		{"flatten-pdf", func(in, out string) (map[string]interface{}, error) { return flattenPDF(context.Background(), in, out) }, false},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			out := filepath.Join(dir, tc.tool+".pdf")
			meta, err := tc.run(input, out)
			if err != nil {
				t.Fatalf("%s: %v", tc.tool, err)
			}
			assertValidPDF(t, out)
			if tc.fallback {
				if got := extractText(t, out); !strings.Contains(got, "फायरडॉक्स") {
					t.Errorf("Devanagari text lost after %s: %q", tc.tool, got[:min(len(got), 80)])
				}
				if meta["fallback"] != "text-preserving" {
					t.Errorf("metadata fallback = %v, want text-preserving", meta["fallback"])
				}
			} else if meta["textExtractable"] != false || meta["warnings"] == nil {
				t.Errorf("flatten must warn when text breaks; metadata %v", meta)
			}
		})
	}
}

// The guard must not fire on ordinary Latin text, which Ghostscript keeps.
func TestCompressLatinTextNoFallbackBinaries(t *testing.T) {
	dir := t.TempDir()
	input := buildOfficeTextPDF(t, dir, strings.Repeat("The quarterly invoice summary lists every payment and its delivery date.\n", 20))
	out := filepath.Join(dir, "out.pdf")
	meta, err := compressPDF(context.Background(), input, out, map[string]interface{}{"quality": "medium"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := meta["fallback"]; ok {
		t.Errorf("Latin text must not trigger the fallback; metadata %v", meta)
	}
	if !strings.Contains(extractText(t, out), "quarterly invoice") {
		t.Error("Latin text lost")
	}
}

// twoPagePDF hand-assembles a valid 2-page Helvetica PDF whose Pages node
// (object 2) is written last, the layout the corruptions below rely on.
// NOTE: same construction as scripts/bench-engine/internal/corpus/gen_pdf.go
// buildTextPDF (cross-module import not allowed).
func twoPagePDF() []byte {
	var buf bytes.Buffer
	offsets := []int{0}
	obj := func(body string) int {
		offsets = append(offsets, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", len(offsets)-1, body)
		return len(offsets) - 1
	}
	buf.WriteString("%PDF-1.7\n")
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	offsets = append(offsets, 0) // object 2, written below
	font := obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	var kids []string
	for p := 1; p <= 2; p++ {
		cs := fmt.Sprintf("BT /F1 18 Tf 72 700 Td (Page %d of the guard test) Tj ET", p)
		stream := obj(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(cs), cs))
		page := obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 %d 0 R >> >> /Contents %d 0 R >>", font, stream))
		kids = append(kids, fmt.Sprintf("%d 0 R", page))
	}
	offsets[2] = buf.Len()
	fmt.Fprintf(&buf, "2 0 obj\n<< /Type /Pages /Kids [%s] /Count 2 >>\nendobj\n", strings.Join(kids, " "))
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, off := range offsets[1:] {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return buf.Bytes()
}

func TestRewritesKeepPagesOfCorruptInputBinaries(t *testing.T) {
	base := twoPagePDF()
	corrupt := map[string][]byte{
		// A 5000-deep array in the catalog: every later xref offset is wrong
		// and the catalog breaks parser nesting limits; the pages are intact.
		"deep-nesting": bytes.Replace(base, []byte("/Type /Catalog"),
			[]byte("/Type /Catalog /Deep "+strings.Repeat("[", 5000)+strings.Repeat("]", 5000)), 1),
		// The Pages node lists itself as parent and first kid; the two real
		// leaves follow and match /Count 2.
		"cyclic-pages": bytes.Replace(base, []byte("/Type /Pages /Kids ["),
			[]byte("/Type /Pages /Parent 2 0 R /Kids [2 0 R "), 1),
	}
	for name, data := range corrupt {
		dir := t.TempDir()
		input := filepath.Join(dir, name+".pdf")
		if err := os.WriteFile(input, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if n, err := pdfPageCount(context.Background(), input); err != nil || n != 2 {
			t.Fatalf("%s fixture: Poppler sees %d pages (err %v), want 2", name, n, err)
		}
		for tool, run := range map[string]func(in, out string) error{
			"compress-pdf": func(in, out string) error {
				_, err := compressPDF(context.Background(), in, out, nil)
				return err
			},
			"repair-pdf": func(in, out string) error { _, err := repairPDF(context.Background(), in, out); return err },
		} {
			t.Run(name+"/"+tool, func(t *testing.T) {
				out := filepath.Join(dir, tool+".pdf")
				if err := run(input, out); err != nil {
					t.Fatalf("%s: %v", tool, err)
				}
				if n, err := pdfPageCount(context.Background(), out); err != nil || n != 2 {
					t.Errorf("%s kept %d pages (err %v), want 2", tool, n, err)
				}
			})
		}
	}
}

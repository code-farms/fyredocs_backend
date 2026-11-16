package corpus

import (
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
)

// buildTextPDF writes a minimal, valid PDF: pages of Helvetica text, plus an
// optional incompressible padding stream so size classes (small/medium/large)
// can be hit without changing the page content. It is hand-assembled so the
// harness needs no PDF writer dependency for its most common input; the result
// is checked by pdfcpu in the unit tests.
func buildTextPDF(r *rand.Rand, pages, linesPerPage, padBytes int) []byte {
	var buf bytes.Buffer
	offsets := []int{0}
	obj := func(body string) int {
		offsets = append(offsets, buf.Len())
		n := len(offsets) - 1
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", n, body)
		return n
	}
	buf.WriteString("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	catalog := obj("<< /Type /Catalog /Pages 2 0 R >>")
	_ = catalog
	// Object 2 (Pages) is written after the page objects; reserve its slot.
	offsets = append(offsets, 0)
	pagesObj := len(offsets) - 1
	font := obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	var kids []string
	for p := 0; p < pages; p++ {
		var content strings.Builder
		content.WriteString("BT /F1 11 Tf 56 760 Td 14 TL\n")
		fmt.Fprintf(&content, "(Page %d) Tj T*\n", p+1)
		for l := 0; l < linesPerPage; l++ {
			fmt.Fprintf(&content, "(%s) Tj T*\n", sentence(r, 9))
		}
		content.WriteString("ET")
		cs := content.String()
		stream := obj(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(cs), cs))
		page := obj(fmt.Sprintf("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 %d 0 R >> >> /Contents %d 0 R >>", pagesObj, font, stream))
		kids = append(kids, fmt.Sprintf("%d 0 R", page))
	}
	if padBytes > 0 {
		pad := make([]byte, padBytes)
		for i := range pad {
			pad[i] = byte(r.UintN(256))
		}
		offsets = append(offsets, buf.Len())
		n := len(offsets) - 1
		fmt.Fprintf(&buf, "%d 0 obj\n<< /Length %d >>\nstream\n", n, len(pad))
		buf.Write(pad)
		buf.WriteString("\nendstream\nendobj\n")
	}
	offsets[pagesObj] = buf.Len()
	fmt.Fprintf(&buf, "%d 0 obj\n<< /Type /Pages /Kids [%s] /Count %d >>\nendobj\n", pagesObj, strings.Join(kids, " "), pages)
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, off := range offsets[1:] {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return buf.Bytes()
}

// sizeClassBytes maps the k6 size classes (scripts/k6/fixtures/gen.py TARGETS)
// to padding targets.
var sizeClassBytes = map[string]int{"small": 800_000, "medium": 8_000_000, "large": 40_000_000}

// genTextPDF: params pages (default 5), lines (default 40), size (small|medium|
// large|none, default none = no padding).
func genTextPDF(_ context.Context, g *Gen, it Item) (string, error) {
	r := rng(it)
	pages := intParam(it, "pages", 5)
	data := buildTextPDF(r, pages, intParam(it, "lines", 40), 0)
	if sc := param(it, "size", "none"); sc != "none" {
		target, ok := sizeClassBytes[sc]
		if !ok {
			return "", errUnsupported(it, "size "+sc)
		}
		if pad := target - len(data); pad > 0 {
			data = buildTextPDF(rng(it), pages, intParam(it, "lines", 40), pad)
		}
	}
	abs, rel, err := g.outPath(it, "pdf")
	if err != nil {
		return "", err
	}
	return rel, os.WriteFile(abs, data, 0o644)
}

package corpus

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/image/draw"
	"golang.org/x/image/math/f64"
)

// These generators shell out to the oracle tools in fyredocs-test-binaries
// (pdftoppm, soffice, python3). `gen` therefore runs in that image; on a host
// without them these generators fail with a clear "not found" error.

// run executes name with args under ctx and returns combined output on failure.
func run(ctx context.Context, name string, args ...string) error {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// convertWithLibreOffice converts src to the format `to` (a soffice
// --convert-to target such as "pdf" or "docx") into dir, with a fresh profile so
// the LibreOffice used for generation never shares state with anything else.
func convertWithLibreOffice(ctx context.Context, scratch, src, to, dir string) (string, error) {
	profile, err := os.MkdirTemp(scratch, "lo-profile-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(profile)
	u := url.URL{Scheme: "file", Path: profile}
	args := []string{"-env:UserInstallation=" + u.String(), "--headless"}
	if strings.EqualFold(filepath.Ext(src), ".html") {
		// Without this, .html opens in Writer/Web, which has no DOCX or ODT
		// export filter ("produced no docx"); Writer proper has all of them.
		args = append(args, "--infilter=HTML (StarWriter)")
	}
	args = append(args, "--convert-to", to, "--outdir", dir, src)
	if err := run(ctx, "soffice", args...); err != nil {
		return "", err
	}
	ext := to
	if i := strings.IndexByte(to, ':'); i > 0 {
		ext = to[:i]
	}
	out := filepath.Join(dir, strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))+"."+ext)
	if fi, err := os.Stat(out); err != nil || fi.Size() == 0 {
		return "", fmt.Errorf("soffice produced no %s for %s", ext, filepath.Base(src))
	}
	return out, nil
}

// devanagari is fixed Hindi text for the Devanagari category: a font without
// Devanagari glyphs renders it as boxes, which the tofu check detects.
const devanagari = "यह फायरडॉक्स का हिंदी परीक्षण दस्तावेज़ है। भुगतान की स्थिति, वितरण की तारीख और कुल राशि नीचे दी गई है।"

// loSource writes the source document for a LibreOffice series. Kinds:
// "text" (plain paragraphs), "hindi" (Devanagari paragraphs), "table" (an HTML
// table whose cells are known, the labelled-table stand-in), "columns" (a
// two-column HTML layout), "sheet" (CSV, param rows), "repo" (a repo file,
// param path).
func loSource(g *Gen, it Item) (string, error) {
	r := rng(it)
	var name, body string
	switch kind := param(it, "source", "text"); kind {
	case "text", "hindi":
		name = it.ID + ".txt"
		var b strings.Builder
		for i := 0; i < intParam(it, "paragraphs", 30); i++ {
			if kind == "hindi" {
				b.WriteString(devanagari + "\n\n")
			} else {
				b.WriteString(sentence(r, 60) + "\n\n")
			}
		}
		body = b.String()
	case "table":
		name = it.ID + ".html"
		var b strings.Builder
		b.WriteString(`<!doctype html><html><head><meta charset="utf-8"></head><body><table border="1"><tr><th>Item</th><th>Qty</th><th>Amount</th></tr>`)
		for i := 0; i < intParam(it, "rows", 40); i++ {
			fmt.Fprintf(&b, "<tr><td>%s</td><td>%d</td><td>%d.%02d</td></tr>", words[r.IntN(len(words))], r.IntN(90)+1, r.IntN(9000)+100, r.IntN(100))
		}
		b.WriteString("</table></body></html>")
		body = b.String()
	case "columns":
		name = it.ID + ".html"
		var b strings.Builder
		b.WriteString(`<!doctype html><html><head><meta charset="utf-8"></head><body><table width="100%"><tr>`)
		for c := 0; c < 2; c++ {
			b.WriteString(`<td valign="top" width="50%">`)
			for p := 0; p < 8; p++ {
				fmt.Fprintf(&b, "<p>%s</p>", sentence(r, 45))
			}
			b.WriteString("</td>")
		}
		b.WriteString("</tr></table></body></html>")
		body = b.String()
	case "repo":
		// An existing repo file (param path, relative to the repo root), e.g. a
		// k6 fixture that gen.py only produces in other formats.
		data, err := os.ReadFile(filepath.Join(g.RepoDir, param(it, "path", "")))
		if err != nil {
			return "", err
		}
		name = it.ID + filepath.Ext(param(it, "path", ""))
		body = string(data)
	case "sheet":
		name = it.ID + ".csv"
		var b strings.Builder
		b.WriteString("id,item,qty,unit_price,total\n")
		for i := 1; i <= intParam(it, "rows", 1000); i++ {
			q, p := r.IntN(90)+1, r.IntN(9000)+100
			fmt.Fprintf(&b, "%d,%s,%d,%d,%d\n", i, words[r.IntN(len(words))], q, p, q*p)
		}
		body = b.String()
	default:
		return "", errUnsupported(it, "source "+kind)
	}
	p := filepath.Join(g.Scratch, name)
	return p, os.WriteFile(p, []byte(body), 0o644)
}

// genLibreOffice: params source (see loSource), to (soffice target, e.g. pdf,
// docx, xlsx, odt, ods). Office-format sources are converted by the same
// LibreOffice the oracles use.
func genLibreOffice(ctx context.Context, g *Gen, it Item) (string, error) {
	src, err := loSource(g, it)
	if err != nil {
		return "", err
	}
	defer os.Remove(src)
	to := param(it, "to", "pdf")
	dir, err := os.MkdirTemp(g.Scratch, "lo-out-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	out, err := convertWithLibreOffice(ctx, g.Scratch, src, to, dir)
	if err != nil {
		return "", fmt.Errorf("%s: %w", it.ID, err)
	}
	ext := strings.TrimPrefix(filepath.Ext(out), ".")
	abs, rel, err := g.outPath(it, ext)
	if err != nil {
		return "", err
	}
	return rel, moveFile(out, abs)
}

// moveFile renames src to dst, copying when they are on different filesystems
// (the scratch dir is the container's /tmp, the corpus a bind mount: EXDEV).
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return err
	}
	return os.Remove(src)
}

// genScan: renders a generated text PDF with pdftoppm at param dpi (default
// 300), turns each page by a small seeded angle and adds noise, and imports the
// pages as JPEGs into a PDF — a stand-in for a phone or flatbed scan.
func genScan(ctx context.Context, g *Gen, it Item) (string, error) {
	r := rng(it)
	dir, err := os.MkdirTemp(g.Scratch, "scan-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	src := filepath.Join(dir, "src.pdf")
	if err := os.WriteFile(src, buildTextPDF(r, intParam(it, "pages", 2), 40, 0), 0o644); err != nil {
		return "", err
	}
	// Keep the true text next to the scan (<id>.src.txt) so OCR output can be
	// scored against what the page actually says.
	abs0, _, err := g.outPath(it, "src.txt")
	if err != nil {
		return "", err
	}
	if err := run(ctx, "pdftotext", "-layout", src, abs0); err != nil {
		return "", err
	}
	dpi := param(it, "dpi", "300")
	if err := run(ctx, "pdftoppm", "-r", dpi, "-gray", "-png", src, filepath.Join(dir, "p")); err != nil {
		return "", err
	}
	pngs, _ := filepath.Glob(filepath.Join(dir, "p-*.png"))
	sort.Strings(pngs)
	var jpgs []string
	for i, p := range pngs {
		f, err := os.Open(p)
		if err != nil {
			return "", err
		}
		page, err := png.Decode(f)
		f.Close()
		if err != nil {
			return "", err
		}
		angle := (r.Float64()*4 - 2) * math.Pi / 180 // ±2°
		rot := image.NewRGBA(page.Bounds())
		draw.Draw(rot, rot.Bounds(), image.White, image.Point{}, draw.Src)
		b := page.Bounds()
		cx, cy := float64(b.Dx())/2, float64(b.Dy())/2
		s, c := math.Sin(angle), math.Cos(angle)
		draw.BiLinear.Transform(rot, f64.Aff3{c, -s, cx - c*cx + s*cy, s, c, cy - s*cx - c*cy}, page, b, draw.Over, nil)
		for y := 0; y < b.Dy(); y += 3 {
			for x := (y * 7) % 5; x < b.Dx(); x += 5 {
				if r.IntN(10) == 0 {
					rot.Pix[(y*rot.Stride)+x*4] -= uint8(r.IntN(60))
				}
			}
		}
		data, err := encodeImage(rot, "jpeg")
		if err != nil {
			return "", err
		}
		jp := filepath.Join(dir, fmt.Sprintf("s-%02d.jpg", i))
		if err := os.WriteFile(jp, data, 0o644); err != nil {
			return "", err
		}
		jpgs = append(jpgs, jp)
	}
	abs, rel, err := g.outPath(it, "pdf")
	if err != nil {
		return "", err
	}
	return rel, importOnLetter(ctx, jpgs, abs)
}

// genK6Fixture copies one of the k6 load-test fixtures (param kind, e.g.
// docx/xlsx/pptx/odt/ods/odp/png, and size small|medium|large), generating
// them with scripts/k6/fixtures/gen.py when absent. Those fixtures are the
// existing office inputs, built from raw OOXML/ODF parts by the stdlib script.
func genK6Fixture(ctx context.Context, g *Gen, it Item) (string, error) {
	kind, size := param(it, "kind", "docx"), param(it, "size", "small")
	ext := map[string]string{"image": "png"}[kind]
	if ext == "" {
		ext = kind
	}
	src := filepath.Join(g.RepoDir, "scripts/k6/fixtures/out", kind, size+"."+ext)
	if _, err := os.Stat(src); err != nil {
		if err := run(ctx, "python3", filepath.Join(g.RepoDir, "scripts/k6/fixtures/gen.py")); err != nil {
			return "", err
		}
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	abs, rel, err := g.outPath(it, ext)
	if err != nil {
		return "", err
	}
	return rel, os.WriteFile(abs, data, 0o644)
}

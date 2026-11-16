// Package oracle wraps the reference tools of the test image — Poppler,
// LibreOffice and veraPDF — that the differential checks compare a tool's output
// against (doc 16 §5.6). They run only inside fyredocs-test-binaries; nothing
// here ships in a service image.
package oracle

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"bench-engine/internal/diff"
)

// exe runs name with args and returns stdout; stderr is folded into the error.
func exe(ctx context.Context, name string, args ...string) ([]byte, error) {
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

// pageSizeRE matches pdfinfo's per-page lines: "Page    3 size: 612 x 792 pts".
var pageSizeRE = regexp.MustCompile(`(?m)^Page\s+(\d+) size:\s+([\d.]+) x ([\d.]+) pts`)

// pageRotRE matches "Page    3 rot:  90": pdfinfo reports the MediaBox size and
// the /Rotate value separately, so a rotated page keeps its unrotated size.
var pageRotRE = regexp.MustCompile(`(?m)^Page\s+(\d+) rot:\s+(-?\d+)`)

// Info is what pdfinfo reports about a PDF.
type Info struct {
	Pages int
	Sizes []diff.PageSize // per page as displayed (/Rotate applied), 1-based order
}

// PDFInfo reads page count and every page's size. password, if set, is the
// user password (-upw).
func PDFInfo(ctx context.Context, path, password string) (Info, error) {
	args := []string{"-f", "1", "-l", "100000"}
	if password != "" {
		args = append(args, "-upw", password)
	}
	out, err := exe(ctx, "pdfinfo", append(args, path)...)
	if err != nil {
		return Info{}, err
	}
	return parseInfo(out), nil
}

// parseInfo turns pdfinfo's per-page output into displayed page sizes.
func parseInfo(out []byte) Info {
	rot := map[string]int{}
	for _, m := range pageRotRE.FindAllSubmatch(out, -1) {
		r, _ := strconv.Atoi(string(m[2]))
		rot[string(m[1])] = ((r % 360) + 360) % 360
	}
	var in Info
	for _, m := range pageSizeRE.FindAllSubmatch(out, -1) {
		w, _ := strconv.ParseFloat(string(m[2]), 64)
		h, _ := strconv.ParseFloat(string(m[3]), 64)
		ps := diff.PageSize{W: w, H: h}
		if rot[string(m[1])]%180 == 90 {
			ps = diff.Rotated(ps)
		}
		in.Sizes = append(in.Sizes, ps)
	}
	in.Pages = len(in.Sizes)
	return in
}

// Text extracts page's text (1-based) in layout mode. password, if set, is the
// user password.
func Text(ctx context.Context, path, password string, page int) (string, error) {
	p := strconv.Itoa(page)
	out, err := exe(ctx, "pdftotext", append(pwArgs(password), "-f", p, "-l", p, "-layout", path, "-")...)
	return string(out), err
}

// pwArgs is the Poppler user-password flag, or nothing.
func pwArgs(password string) []string {
	if password == "" {
		return nil
	}
	return []string{"-upw", password}
}

// AllTextRaw extracts the whole document's text in content-stream order. OCR
// text layers on skewed scans come out with words split ("docume nt") under
// -layout, which rebuilds lines from positions; raw order is what a reader's
// copy and search see.
func AllTextRaw(ctx context.Context, path string) (string, error) {
	out, err := exe(ctx, "pdftotext", "-raw", path, "-")
	return string(out), err
}

// AllText extracts the whole document's text.
func AllText(ctx context.Context, path string) (string, error) {
	out, err := exe(ctx, "pdftotext", "-layout", path, "-")
	return string(out), err
}

// Render rasterises page (1-based) to greyscale at dpi. password, if set, is
// the user password.
func Render(ctx context.Context, path, password string, page, dpi int, scratch string) (*image.Gray, error) {
	dir, err := os.MkdirTemp(scratch, "render-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	p := strconv.Itoa(page)
	if _, err := exe(ctx, "pdftoppm", append(pwArgs(password), "-f", p, "-l", p, "-r", strconv.Itoa(dpi), "-gray", "-png", "-singlefile", path, filepath.Join(dir, "page"))...); err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(dir, "page.png"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, err
	}
	if g, ok := img.(*image.Gray); ok {
		return g, nil
	}
	g := image.NewGray(img.Bounds())
	draw.Draw(g, g.Bounds(), img, img.Bounds().Min, draw.Src)
	return g, nil
}

// Font is one pdffonts row.
type Font struct {
	Name     string
	Embedded bool
}

// Fonts lists the document's fonts with pdffonts. Columns are fixed-width, so
// the embedded flag is read from the column under the "emb" header.
func Fonts(ctx context.Context, path string) ([]Font, error) {
	out, err := exe(ctx, "pdffonts", path)
	if err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	var header string
	var fonts []Font
	for sc.Scan() {
		line := sc.Text()
		if header == "" {
			header = line
			continue
		}
		if strings.HasPrefix(line, "---") {
			continue
		}
		emb := strings.Index(header, "emb")
		if emb < 0 || len(line) < emb+3 {
			continue
		}
		name := strings.Fields(line)
		if len(name) == 0 {
			continue
		}
		fonts = append(fonts, Font{Name: name[0], Embedded: strings.TrimSpace(line[emb:emb+3]) == "yes"})
	}
	sort.Slice(fonts, func(i, j int) bool { return fonts[i].Name < fonts[j].Name })
	return fonts, nil
}

// VeraPDF validates path against PDF/A-2b and reports whether it passed.
func VeraPDF(ctx context.Context, path string) (bool, string, error) {
	out, err := exe(ctx, "verapdf", "--flavour", "2b", "--format", "text", path)
	s := string(out)
	if strings.Contains(s, "PASS ") {
		return true, "", nil
	}
	if strings.Contains(s, "FAIL ") {
		return false, strings.TrimSpace(s), nil
	}
	return false, s, err
}

// LibreOfficeRef converts src to PDF with a fresh LibreOffice profile: the
// reference an office/html→PDF output is compared against. The test image's
// LibreOffice is the version the convert-to-pdf image ships (both Alpine 3.24).
func LibreOfficeRef(ctx context.Context, src, scratch string) (string, error) {
	dir, err := os.MkdirTemp(scratch, "loref-")
	if err != nil {
		return "", err
	}
	profile := filepath.Join(dir, "profile")
	u := url.URL{Scheme: "file", Path: profile}
	if _, err := exe(ctx, "soffice", "-env:UserInstallation="+u.String(), "--headless", "--convert-to", "pdf", "--outdir", dir, src); err != nil {
		return "", err
	}
	out := filepath.Join(dir, strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))+".pdf")
	if _, err := os.Stat(out); err != nil {
		return "", fmt.Errorf("LibreOffice produced no reference PDF for %s", filepath.Base(src))
	}
	return out, nil
}

// Package check decides, per tool, which differential checks apply to a job's
// output and runs them against the oracles. Each Result carries the measured
// value and a pass flag at the slice's default threshold; release gating
// compares values between runs (package report), so a threshold here is a
// readable default, not the gate.
package check

import (
	"archive/zip"
	"context"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"bench-engine/internal/diff"
	"bench-engine/internal/oracle"
)

// Result is one check's outcome. NA is set (and Pass ignored) when the check
// could not apply, with the reason.
type Result struct {
	Name   string  `json:"name"`
	Value  float64 `json:"value"`
	Pass   bool    `json:"pass"`
	Detail string  `json:"detail,omitempty"`
	NA     string  `json:"na,omitempty"`
}

// Input describes one finished job.
type Input struct {
	Tool       string
	Category   string
	Inputs     []string // corpus files sent to the tool
	Output     string   // downloaded output
	Password   string   // the input's user password (unlock-pdf)
	SourceText string   // true text of a generated scan, "" if not applicable
	Scratch    string
}

// thresholds are the per-check pass defaults of this slice. SSIM for compress
// is lower because it may downsample images by design.
const (
	textCERMax      = 0.02
	wordRecallMin   = 0.98
	roundTripMin    = 0.90
	ocrRecallMin    = 0.90
	ssimSame        = 0.97
	ssimCompress    = 0.90
	ssimLibreOffice = 0.95
	pagesToRender   = 3
	renderDPI       = 72
)

// samePageTools keep pages, sizes and text of the input.
var samePageTools = map[string]bool{
	"compress-pdf": true, "repair-pdf": true, "pdf-metadata": true, "unlock-pdf": true,
	"ocr-pdf": true, "pdf-to-pdfa": true,
}

// toPDFTools turn an office or HTML document into PDF; LibreOffice is the reference.
var toPDFTools = map[string]bool{
	"word-to-pdf": true, "excel-to-pdf": true, "powerpoint-to-pdf": true, "odt-to-pdf": true,
	"ods-to-pdf": true, "odp-to-pdf": true, "html-to-pdf": true,
}

// roundTripTools produce an editable document; it is converted back to PDF and
// must keep the input's words.
var roundTripTools = map[string]bool{
	"pdf-to-word": true, "pdf-to-odt": true, "pdf-to-excel": true, "pdf-to-ods": true,
	"word-to-odt": true, "excel-to-ods": true,
}

// Run applies every check that fits in.Tool.
func Run(ctx context.Context, in Input) []Result {
	var rs []Result
	add := func(r Result) { rs = append(rs, r) }
	src := in.Inputs[0]
	switch {
	case samePageTools[in.Tool]:
		pw := ""
		if in.Tool == "unlock-pdf" {
			pw = in.Password
		}
		pages := pagesAndSizes(ctx, src, pw, in.Output, 1, false)
		add(pages)
		if strings.HasPrefix(pages.Detail, "pages ") {
			// Page count differs: per-page comparisons would only repeat it.
			add(Result{Name: "text_cer", NA: "page count differs (" + pages.Detail + ")"})
			add(Result{Name: "ssim", NA: "page count differs (" + pages.Detail + ")"})
			break
		}
		if in.Tool == "ocr-pdf" && in.SourceText != "" {
			out, err := oracle.AllTextRaw(ctx, in.Output)
			add(score("ocr_word_recall", diff.WordRecall(in.SourceText, out), ocrRecallMin, err))
		} else if in.Tool != "ocr-pdf" {
			add(textCER(ctx, src, pw, in.Output))
		}
		min := ssimSame
		if in.Tool == "compress-pdf" {
			min = ssimCompress
		}
		add(ssim(ctx, src, pw, in.Output, min, in.Scratch))
		if in.Tool == "pdf-to-pdfa" {
			ok, detail, err := oracle.VeraPDF(ctx, in.Output)
			add(Result{Name: "verapdf_2b", Value: b2f(ok), Pass: ok && err == nil, Detail: firstLine(detail, err)})
			add(fontsEmbedded(ctx, in.Output))
		}
	case in.Tool == "rotate-pdf":
		add(pagesAndSizes(ctx, src, "", in.Output, 1, true))
	case in.Tool == "merge-pdf":
		add(pagesAndSizes(ctx, src, "", in.Output, len(in.Inputs), false))
	case in.Tool == "protect-pdf":
		_, errNoPW := oracle.PDFInfo(ctx, in.Output, "")
		info, errPW := oracle.PDFInfo(ctx, in.Output, "bench-protect-1")
		ok := errNoPW != nil && errPW == nil && info.Pages > 0
		add(Result{Name: "password_required", Value: b2f(ok), Pass: ok, Detail: "opens only with the password"})
	case toPDFTools[in.Tool]:
		ref, err := oracle.LibreOfficeRef(ctx, src, in.Scratch)
		if err != nil {
			add(Result{Name: "libreoffice_reference", NA: err.Error()})
			break
		}
		add(pagesMatch(ctx, ref, in.Output))
		refText, err1 := oracle.AllText(ctx, ref)
		outText, err2 := oracle.AllText(ctx, in.Output)
		add(score("word_recall_vs_libreoffice", diff.WordRecall(refText, outText), wordRecallMin, firstErr(err1, err2)))
		add(ssim(ctx, ref, "", in.Output, ssimLibreOffice, in.Scratch))
		add(fontsEmbedded(ctx, in.Output))
	case in.Tool == "image-to-pdf" || in.Tool == "scan-to-pdf":
		info, err := oracle.PDFInfo(ctx, in.Output, "")
		add(score("pages_equal", b2f(err == nil && info.Pages == len(in.Inputs)), 1, err))
	case in.Tool == "pdf-to-text":
		ref, err1 := oracle.AllText(ctx, src)
		out, err2 := os.ReadFile(in.Output)
		add(score("word_recall", diff.WordRecall(ref, string(out)), 0.99, firstErr(err1, err2)))
	case roundTripTools[in.Tool]:
		back, err := oracle.LibreOfficeRef(ctx, in.Output, in.Scratch)
		if err != nil {
			add(Result{Name: "round_trip_recall", NA: "output could not be converted back: " + err.Error()})
			break
		}
		refPDF := src
		if !strings.HasSuffix(strings.ToLower(src), ".pdf") {
			if refPDF, err = oracle.LibreOfficeRef(ctx, src, in.Scratch); err != nil {
				add(Result{Name: "round_trip_recall", NA: err.Error()})
				break
			}
		}
		ref, err1 := oracle.AllText(ctx, refPDF)
		out, err2 := oracle.AllText(ctx, back)
		add(score("round_trip_recall", diff.WordRecall(ref, out), roundTripMin, firstErr(err1, err2)))
	case in.Tool == "pdf-to-image":
		info, err := oracle.PDFInfo(ctx, src, "")
		n, zerr := imageCount(in.Output)
		add(score("images_equal_pages", b2f(err == nil && zerr == nil && n == info.Pages), 1, firstErr(err, zerr)))
	case in.Tool == "split-pdf":
		n, err := zipEntries(in.Output)
		add(score("split_parts", b2f(err == nil && n > 0), 1, err))
	default:
		add(Result{Name: "no_checks", NA: "no differential check defined for " + in.Tool})
	}
	if in.Category == "hindi" && strings.HasSuffix(strings.ToLower(in.Output), ".pdf") {
		add(devanagariRendered(ctx, in.Output))
	}
	return rs
}

// pagesAndSizes checks the output has want× the input's pages and, for a
// single-input tool, the same (or rotated) page sizes.
func pagesAndSizes(ctx context.Context, src, srcPW, out string, factor int, rotated bool) Result {
	a, err := oracle.PDFInfo(ctx, src, srcPW)
	if err != nil {
		return Result{Name: "pages_and_sizes", NA: "input: " + err.Error()}
	}
	b, err := oracle.PDFInfo(ctx, out, "")
	if err != nil {
		return Result{Name: "pages_and_sizes", Detail: "output unreadable: " + err.Error()}
	}
	if b.Pages != a.Pages*factor {
		return Result{Name: "pages_and_sizes", Detail: fmt.Sprintf("pages %d, want %d", b.Pages, a.Pages*factor)}
	}
	if factor == 1 {
		for i := range a.Sizes {
			want := a.Sizes[i]
			if rotated {
				want = diff.Rotated(want)
			}
			if !diff.SameSize(want, b.Sizes[i]) {
				return Result{Name: "pages_and_sizes", Detail: fmt.Sprintf("page %d size %v, want %v", i+1, b.Sizes[i], want)}
			}
		}
	}
	return Result{Name: "pages_and_sizes", Value: 1, Pass: true}
}

// pagesMatch checks two PDFs have the same page count.
func pagesMatch(ctx context.Context, ref, out string) Result {
	a, err1 := oracle.PDFInfo(ctx, ref, "")
	b, err2 := oracle.PDFInfo(ctx, out, "")
	if err := firstErr(err1, err2); err != nil {
		return Result{Name: "pages_vs_reference", Detail: err.Error()}
	}
	return Result{Name: "pages_vs_reference", Value: float64(b.Pages), Pass: a.Pages == b.Pages, Detail: fmt.Sprintf("reference %d", a.Pages)}
}

// textCER is the mean per-page CER of the output against the input, over the
// first pagesToRender pages.
func textCER(ctx context.Context, src, srcPW, out string) Result {
	info, err := oracle.PDFInfo(ctx, src, srcPW)
	if err != nil {
		return Result{Name: "text_cer", NA: err.Error()}
	}
	n := min(info.Pages, pagesToRender)
	var sum float64
	for p := 1; p <= n; p++ {
		a, err1 := oracle.Text(ctx, src, srcPW, p)
		b, err2 := oracle.Text(ctx, out, "", p)
		if err := firstErr(err1, err2); err != nil {
			return Result{Name: "text_cer", Detail: err.Error()}
		}
		sum += diff.CER(a, b)
	}
	cer := sum / float64(max(n, 1))
	return Result{Name: "text_cer", Value: cer, Pass: cer <= textCERMax}
}

// ssim is the mean SSIM of the first pagesToRender pages at renderDPI.
func ssim(ctx context.Context, ref, refPW, out string, min float64, scratch string) Result {
	info, err := oracle.PDFInfo(ctx, ref, refPW)
	if err != nil {
		return Result{Name: "ssim", NA: err.Error()}
	}
	n := minInt(info.Pages, pagesToRender)
	var sum float64
	for p := 1; p <= n; p++ {
		a, err1 := oracle.Render(ctx, ref, refPW, p, renderDPI, scratch)
		b, err2 := oracle.Render(ctx, out, "", p, renderDPI, scratch)
		if err := firstErr(err1, err2); err != nil {
			return Result{Name: "ssim", Detail: err.Error()}
		}
		a, b = cropCommon(a, b, ssimSlackPx)
		s, err := diff.SSIM(a, b)
		if err != nil {
			return Result{Name: "ssim", Detail: fmt.Sprintf("page %d: %v", p, err)}
		}
		sum += s
	}
	v := sum / float64(max(n, 1))
	return Result{Name: "ssim", Value: v, Pass: v >= min}
}

// fontsEmbedded checks every font in the output is embedded.
func fontsEmbedded(ctx context.Context, out string) Result {
	fonts, err := oracle.Fonts(ctx, out)
	if err != nil {
		return Result{Name: "fonts_embedded", Detail: err.Error()}
	}
	var missing []string
	for _, f := range fonts {
		if !f.Embedded {
			missing = append(missing, f.Name)
		}
	}
	return Result{Name: "fonts_embedded", Value: b2f(len(missing) == 0), Pass: len(missing) == 0, Detail: strings.Join(missing, ", ")}
}

// devanagariRendered is the no-tofu check for Hindi documents: a Devanagari
// font is embedded and the text layer holds Devanagari. A Latin-only fallback
// font draws boxes and drops the text (the Q6 failure), so both conditions are
// needed; this proxy replaced pHash-matching rendered .notdef boxes, which is
// fragile across fonts.
func devanagariRendered(ctx context.Context, out string) Result {
	fonts, err := oracle.Fonts(ctx, out)
	if err != nil {
		return Result{Name: "devanagari_rendered", Detail: err.Error()}
	}
	font := false
	for _, f := range fonts {
		if f.Embedded && strings.Contains(strings.ToLower(f.Name), "devanagari") {
			font = true
		}
	}
	text, _ := oracle.AllText(ctx, out)
	has := strings.IndexFunc(text, func(r rune) bool { return unicode.In(r, unicode.Devanagari) }) >= 0
	return Result{Name: "devanagari_rendered", Value: b2f(font && has), Pass: font && has,
		Detail: fmt.Sprintf("devanagari font embedded=%v, devanagari text=%v", font, has)}
}

// imageCount counts PNG/JPEG images in a zip, or 1 for a single image file.
func imageCount(path string) (int, error) {
	if !strings.HasSuffix(strings.ToLower(path), ".zip") {
		return 1, nil
	}
	return zipEntries(path)
}

// zipEntries counts the files in a zip.
func zipEntries(path string) (int, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return 0, err
	}
	defer zr.Close()
	n := 0
	for _, f := range zr.File {
		if !f.FileInfo().IsDir() {
			n++
		}
	}
	return n, nil
}

// score builds a Result for a value against a minimum.
func score(name string, v, min float64, err error) Result {
	if err != nil {
		return Result{Name: name, Detail: err.Error()}
	}
	return Result{Name: name, Value: v, Pass: v >= min}
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func minInt(a, b int) int { return min(a, b) }

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

func firstLine(s string, err error) string {
	if err != nil {
		return err.Error()
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// SourceTextFor returns the generated scan's true text (<file>.src.txt beside
// the corpus file), or "" when there is none.
func SourceTextFor(corpusFile string) string {
	b, err := os.ReadFile(strings.TrimSuffix(corpusFile, filepath.Ext(corpusFile)) + ".src.txt")
	if err != nil {
		return ""
	}
	return string(b)
}

// ssimSlackPx is how far two renders of the same page may differ in size and
// still be compared: a page 2550.24 pt wide renders one pixel wider than a
// 2550 pt one. Larger differences are a real size change and fail.
const ssimSlackPx = 2

// cropCommon crops a and b to their common top-left area when their sizes
// differ by at most slack pixels in each dimension; otherwise it returns them
// unchanged (and SSIM reports the mismatch).
func cropCommon(a, b *image.Gray, slack int) (*image.Gray, *image.Gray) {
	ab, bb := a.Bounds(), b.Bounds()
	dw, dh := ab.Dx()-bb.Dx(), ab.Dy()-bb.Dy()
	if (dw == 0 && dh == 0) || dw > slack || dw < -slack || dh > slack || dh < -slack {
		return a, b
	}
	w, h := min(ab.Dx(), bb.Dx()), min(ab.Dy(), bb.Dy())
	crop := func(g *image.Gray) *image.Gray {
		r := image.Rect(g.Bounds().Min.X, g.Bounds().Min.Y, g.Bounds().Min.X+w, g.Bounds().Min.Y+h)
		return g.SubImage(r).(*image.Gray)
	}
	return crop(a), crop(b)
}

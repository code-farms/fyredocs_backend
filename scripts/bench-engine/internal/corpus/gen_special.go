package corpus

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// genEncrypted: param mode = rc4-128 | aes-128 | aes-256 | owner-only. The
// passwords are fixed ("bench-user" / "bench-owner") so unlock-pdf runs can
// supply them; owner-only has an empty user password (opens without one).
func genEncrypted(ctx context.Context, g *Gen, it Item) (string, error) {
	plain := filepath.Join(g.Scratch, it.ID+"-plain.pdf")
	if err := os.WriteFile(plain, buildTextPDF(rng(it), 3, 30, 0), 0o644); err != nil {
		return "", err
	}
	defer os.Remove(plain)
	var conf *model.Configuration
	switch m := param(it, "mode", "aes-256"); m {
	case "rc4-128":
		conf = model.NewRC4Configuration("bench-user", "bench-owner", 128)
	case "aes-128":
		conf = model.NewAESConfiguration("bench-user", "bench-owner", 128)
	case "aes-256":
		conf = model.NewAESConfiguration("bench-user", "bench-owner", 256)
	case "owner-only":
		conf = model.NewAESConfiguration("", "bench-owner", 256)
	default:
		return "", errUnsupported(it, "mode "+m)
	}
	abs, rel, err := g.outPath(it, "pdf")
	if err != nil {
		return "", err
	}
	return rel, api.EncryptFile(ctx, plain, abs, conf)
}

// lengthRE finds the first stream /Length value.
var lengthRE = regexp.MustCompile(`<< /Length (\d+) >>`)

// b64 is standard base64, for inline data-URI images in generated HTML.
func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// recoverableModes are the malformed modes pdfcpu repairs by rebuilding the
// cross-reference table (measured 2026-11-13, pdfcpu v0.16.4, strict
// validation): damaged but recoverable files, the case repair-pdf exists for.
// The other modes are structurally fatal. TestMalformedModeClasses pins both.
var recoverableModes = map[string]bool{
	"truncated-no-eof": true, "bad-xref-offsets": true, "no-xref": true, "garbage-header": true,
}

// malformedModes are the corruptions genMalformed cycles through by index; each
// is a class of real-world breakage the repair and parse paths must survive.
var malformedModes = []string{
	"truncated-half", "truncated-no-eof", "bad-xref-offsets", "no-xref",
	"stream-length-short", "stream-length-long", "garbage-header", "deep-nesting",
	"missing-root", "cyclic-pages",
}

// genMalformed: param mode, or (default) the mode at index % len(malformedModes).
// Built from a small valid text PDF; kept under 50 KB (doc 16 §5.2).
func genMalformed(_ context.Context, g *Gen, it Item) (string, error) {
	mode := param(it, "mode", malformedModes[(it.Index-1)%len(malformedModes)])
	base := buildTextPDF(rng(it), 2, 20, 0)
	var out []byte
	switch mode {
	case "truncated-half":
		out = base[:len(base)/2]
	case "truncated-no-eof":
		out = base[:bytes.LastIndex(base, []byte("startxref"))]
	case "bad-xref-offsets":
		x := bytes.Index(base, []byte("xref\n"))
		tail := bytes.ReplaceAll(base[x:], []byte(" 00000 n "), []byte("9 00000 n "))
		out = append(append([]byte{}, base[:x]...), tail...)
	case "no-xref":
		x := bytes.Index(base, []byte("xref\n"))
		out = append(append([]byte{}, base[:x]...), []byte("%%EOF\n")...)
	case "stream-length-short", "stream-length-long":
		// Shift the first content stream's /Length so the declared size no
		// longer matches the bytes: too short cuts the stream, too long runs
		// past "endstream".
		m := lengthRE.FindSubmatchIndex(base)
		n, _ := strconv.Atoi(string(base[m[2]:m[3]]))
		if mode == "stream-length-short" {
			n = max(1, n-40)
		} else {
			n += 400
		}
		out = append(append(append([]byte{}, base[:m[2]]...), []byte(strconv.Itoa(n))...), base[m[3]:]...)
	case "garbage-header":
		out = append([]byte("\x00\x01garbage before header\n"), base...)
	case "deep-nesting":
		deep := strings.Repeat("[", 5000) + strings.Repeat("]", 5000)
		out = bytes.Replace(base, []byte("/Type /Catalog"), []byte("/Type /Catalog /Deep "+deep), 1)
	case "missing-root":
		out = bytes.Replace(base, []byte("/Root 1 0 R"), []byte("/Root 99 0 R"), 1)
	case "cyclic-pages":
		out = bytes.Replace(base, []byte("/Type /Pages /Kids ["), []byte("/Type /Pages /Parent 2 0 R /Kids [2 0 R "), 1)
	default:
		return "", errUnsupported(it, "mode "+mode)
	}
	abs, rel, err := g.outPath(it, "pdf")
	if err != nil {
		return "", err
	}
	return rel, os.WriteFile(abs, out, 0o644)
}

// htmlVariants are the layouts genHTML cycles through by index (doc 16 §5.2:
// flex, grid, images, Devanagari, RTL), plus tables and long documents.
var htmlVariants = []string{"flex", "grid", "image", "devanagari", "rtl", "table", "long"}

// genHTML: param variant, or the variant at index % len(htmlVariants).
func genHTML(_ context.Context, g *Gen, it Item) (string, error) {
	r := rng(it)
	v := param(it, "variant", htmlVariants[(it.Index-1)%len(htmlVariants)])
	var body strings.Builder
	switch v {
	case "flex":
		body.WriteString(`<div style="display:flex;gap:16px">`)
		for i := 0; i < 3; i++ {
			fmt.Fprintf(&body, `<div style="flex:1;border:1px solid #888;padding:8px"><h3>%s</h3><p>%s</p></div>`, sentence(r, 2), sentence(r, 40))
		}
		body.WriteString(`</div>`)
	case "grid":
		body.WriteString(`<div style="display:grid;grid-template-columns:repeat(3,1fr);gap:8px">`)
		for i := 0; i < 9; i++ {
			fmt.Fprintf(&body, `<div style="background:#eef;padding:6px">%s</div>`, sentence(r, 12))
		}
		body.WriteString(`</div>`)
	case "image":
		img, _ := encodeImage(drawImage(r, "shapes", 400, 240), "png")
		fmt.Fprintf(&body, `<p>%s</p><img alt="chart" src="data:image/png;base64,%s"><p>%s</p>`, sentence(r, 30), b64(img), sentence(r, 30))
	case "devanagari":
		body.WriteString(`<h1>हिंदी परीक्षण दस्तावेज़</h1>`)
		for i := 0; i < 6; i++ {
			body.WriteString(`<p>यह फायरडॉक्स का हिंदी परीक्षण है। भुगतान की स्थिति और वितरण की तारीख नीचे दी गई है।</p>`)
		}
	case "rtl":
		body.WriteString(`<div dir="rtl" lang="ar"><h2>تقرير ربع سنوي</h2><p>هذا نص تجريبي باللغة العربية لاختبار الاتجاه من اليمين إلى اليسار.</p></div>`)
		fmt.Fprintf(&body, `<p>%s</p>`, sentence(r, 30))
	case "table":
		body.WriteString(`<table border="1" cellpadding="4"><tr><th>Item</th><th>Qty</th><th>Amount</th></tr>`)
		for i := 0; i < 25; i++ {
			fmt.Fprintf(&body, `<tr><td>%s</td><td>%d</td><td>%d.%02d</td></tr>`, words[r.IntN(len(words))], r.IntN(90)+1, r.IntN(9000)+100, r.IntN(100))
		}
		body.WriteString(`</table>`)
	case "long":
		for i := 0; i < 60; i++ {
			fmt.Fprintf(&body, `<h3>%s %d</h3><p>%s</p>`, sentence(r, 2), i+1, sentence(r, 80))
		}
	default:
		return "", errUnsupported(it, "variant "+v)
	}
	doc := `<!doctype html><html><head><meta charset="utf-8"><title>` + it.ID + `</title></head><body style="font-family:sans-serif">` + body.String() + `</body></html>`
	abs, rel, err := g.outPath(it, "html")
	if err != nil {
		return "", err
	}
	return rel, os.WriteFile(abs, []byte(doc), 0o644)
}

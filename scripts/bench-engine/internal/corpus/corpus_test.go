package corpus

import (
	"context"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

func TestMain(m *testing.M) {
	api.DisableConfigDir()
	os.Exit(m.Run())
}

func newGen(t *testing.T) *Gen {
	t.Helper()
	d := t.TempDir()
	return &Gen{OutDir: filepath.Join(d, "out"), Scratch: d}
}

func item(id, gen string, params map[string]string) Item {
	return Item{ID: id + "-001", Index: 1, Series: Series{ID: id, Category: "test", Generator: gen, Count: 1, Params: params, Tools: []string{"x"}}}
}

func TestManifestValidate(t *testing.T) {
	gens := Generators()
	ok := Manifest{Series: []Series{
		{ID: "a", Category: "pdf", Generator: "textpdf", Count: 2, Tools: []string{"compress-pdf"}},
		{ID: "b", Category: "forms", Status: StatusPending},
	}}
	if err := ok.Validate(gens); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	if got := ok.Items(); len(got) != 2 || got[1].ID != "a-002" {
		t.Errorf("items = %+v", got)
	}
	if p := ok.Pending(); len(p) != 1 || p[0] != "b" {
		t.Errorf("pending = %v", p)
	}
	bad := map[string]Manifest{
		"duplicate id":      {Series: []Series{ok.Series[0], ok.Series[0]}},
		"unknown generator": {Series: []Series{{ID: "c", Generator: "nope", Count: 1, Tools: []string{"x"}}}},
		"zero count":        {Series: []Series{{ID: "c", Generator: "textpdf", Count: 0, Tools: []string{"x"}}}},
		"no tools":          {Series: []Series{{ID: "c", Generator: "textpdf", Count: 1}}},
	}
	for name, m := range bad {
		if err := m.Validate(gens); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestCommittedManifestIsValid(t *testing.T) {
	m, err := Load(filepath.Join("..", "..", "corpus", "manifest.json"), Generators())
	if err != nil {
		t.Fatal(err)
	}
	if n := len(m.Items()); n < 200 {
		t.Errorf("manifest expands to %d files, want >= 200 (doc 16 §5.2)", n)
	}
}

func TestLockVerify(t *testing.T) {
	g := newGen(t)
	it := item("pdf", "textpdf", nil)
	rel, err := genTextPDF(context.Background(), g, it)
	if err != nil {
		t.Fatal(err)
	}
	sum, n, _ := HashFile(filepath.Join(g.OutDir, rel))
	l := Lock{it.ID: {Path: rel, SHA256: sum, Bytes: n, Category: "test"}}
	if errs := Verify(g.OutDir, l, []Item{it}); len(errs) != 0 {
		t.Fatalf("fresh lock: %v", errs)
	}
	p := filepath.Join(g.OutDir, rel)
	b, _ := os.ReadFile(p)
	b[len(b)/2] ^= 1
	os.WriteFile(p, b, 0o644)
	if errs := Verify(g.OutDir, l, []Item{it}); len(errs) != 1 {
		t.Errorf("changed byte: errs = %v, want 1", errs)
	}
	missing := item("other", "textpdf", nil)
	if errs := Verify(g.OutDir, Lock{}, []Item{missing}); len(errs) != 1 {
		t.Errorf("ungenerated item: errs = %v, want 1", errs)
	}
}

func TestTextPDFIsValidAndSized(t *testing.T) {
	g := newGen(t)
	rel, err := genTextPDF(context.Background(), g, item("pdf", "textpdf", map[string]string{"pages": "3", "size": "small"}))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(g.OutDir, rel)
	if err := api.ValidateFile(context.Background(), p, nil, nil); err != nil {
		t.Fatalf("pdfcpu rejects the generated PDF: %v", err)
	}
	if n, _ := api.PageCountFile(context.Background(), p); n != 3 {
		t.Errorf("pages = %d, want 3", n)
	}
	fi, _ := os.Stat(p)
	if fi.Size() < 750_000 || fi.Size() > 900_000 {
		t.Errorf("small size class = %d bytes, want ~800 KB", fi.Size())
	}
}

func TestEncryptedNeedsPassword(t *testing.T) {
	for _, mode := range []string{"rc4-128", "aes-128", "aes-256"} {
		g := newGen(t)
		rel, err := genEncrypted(context.Background(), g, item("enc", "encrypted", map[string]string{"mode": mode}))
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		p := filepath.Join(g.OutDir, rel)
		if _, err := api.ReadContextFile(context.Background(), p); err == nil {
			t.Errorf("%s: opened without a password", mode)
		}
		conf := model.NewDefaultConfiguration()
		conf.UserPW = "bench-user"
		if err := api.ValidateFile(context.Background(), p, conf, nil); err != nil {
			t.Errorf("%s: does not open with the user password: %v", mode, err)
		}
	}
}

// TestMalformedModeClasses: every mode changes the file, stays under 50 KB, and
// lands in its class — fatal modes fail strict validation, recoverable ones are
// repaired by pdfcpu's xref reconstruction.
func TestMalformedModeClasses(t *testing.T) {
	valid := buildTextPDF(rng(item("bad", "malformed", nil)), 2, 20, 0)
	for i, mode := range malformedModes {
		g := newGen(t)
		it := item("bad", "malformed", nil)
		it.Index = i + 1
		rel, err := genMalformed(context.Background(), g, it)
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		p := filepath.Join(g.OutDir, rel)
		b, _ := os.ReadFile(p)
		if string(b) == string(valid) {
			t.Errorf("%s: identical to the valid base", mode)
		}
		if len(b) > 50_000 {
			t.Errorf("%s: %d bytes, want <= 50 KB", mode, len(b))
		}
		conf := model.NewDefaultConfiguration()
		conf.ValidationMode = model.ValidationStrict
		err = api.ValidateFile(context.Background(), p, conf, nil)
		if recoverableModes[mode] && err != nil {
			t.Errorf("%s: expected pdfcpu to repair it, got %v", mode, err)
		}
		if !recoverableModes[mode] && err == nil {
			t.Errorf("%s: fatal mode accepted by strict validation", mode)
		}
	}
}

func TestImagesDecode(t *testing.T) {
	for _, f := range []string{"png", "jpeg"} {
		g := newGen(t)
		rel, err := genImage(context.Background(), g, item("img", "image", map[string]string{"format": f, "w": "200", "h": "100"}))
		if err != nil {
			t.Fatal(err)
		}
		fh, _ := os.Open(filepath.Join(g.OutDir, rel))
		if f == "png" {
			_, err = png.Decode(fh)
		} else {
			_, err = jpeg.Decode(fh)
		}
		fh.Close()
		if err != nil {
			t.Errorf("%s does not decode: %v", f, err)
		}
	}
	g := newGen(t)
	rel, err := genImagePDF(context.Background(), g, item("ipdf", "imagepdf", map[string]string{"images": "3", "w": "300", "h": "200"}))
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := api.PageCountFile(context.Background(), filepath.Join(g.OutDir, rel)); n != 3 {
		t.Errorf("image PDF pages = %d, want 3", n)
	}
}

func TestHTMLVariants(t *testing.T) {
	for i, v := range htmlVariants {
		g := newGen(t)
		it := item("html", "html", nil)
		it.Index = i + 1
		rel, err := genHTML(context.Background(), g, it)
		if err != nil {
			t.Fatalf("%s: %v", v, err)
		}
		b, _ := os.ReadFile(filepath.Join(g.OutDir, rel))
		if !strings.HasPrefix(string(b), "<!doctype html>") {
			t.Errorf("%s: not an HTML document", v)
		}
	}
}

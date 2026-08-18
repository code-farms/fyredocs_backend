//go:build integration && binaries

// Real-binary exec-path tests for the convert-to-pdf worker. No unoserver
// daemon runs in the test container (entrypoint.sh launches those in
// production), so tryUnoconvert exercises the real unoconvert client against
// a dead port and officeToPDF then takes its direct `libreoffice --headless`
// fallback — the exact degraded-daemon shape the fallback exists for. Run via
// `make test-binaries` (dockerized — see deployment/test-binaries.Dockerfile);
// fixtures are generated in-code per
// docs/developer/architecture/testing-strategy.md.
package processing

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// TestMain mirrors main.go's DisableConfigDir: without it, the first pdfcpu
// call from the test runner loads — and on first use writes — a config.yml
// under os.UserConfigDir(), making behavior depend on the container's HOME.
func TestMain(m *testing.M) {
	api.DisableConfigDir()
	os.Exit(m.Run())
}

// assertValidPDFOutput checks the output contract: exists, non-empty, %PDF-
// magic, and pdfcpu validation.
func assertValidPDFOutput(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("output missing: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("output is empty")
	}
	head := make([]byte, 5)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Read(head); err != nil || !bytes.HasPrefix(head, []byte("%PDF-")) {
		t.Fatalf("output lacks %%PDF- header (got %q, err %v)", head, err)
	}
	if err := api.ValidateFile(path, nil); err != nil {
		t.Fatalf("output does not validate: %v", err)
	}
}

// TestHTMLToPDFBinaries drives the full ProcessFile dispatch for html-to-pdf:
// unoconvert is tried first (and fails fast — no daemon), then the direct
// LibreOffice fallback converts the generated HTML.
func TestHTMLToPDFBinaries(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "page.html")
	html := "<html><body><h1>Fyredocs HTML fixture</h1><p>converted by the binaries suite</p></body></html>"
	if err := os.WriteFile(input, []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()

	res, err := ProcessFile(context.Background(), uuid.New(), "html-to-pdf", []string{input}, nil, outDir, nil)
	if err != nil {
		t.Fatalf("ProcessFile html-to-pdf: %v", err)
	}
	if filepath.Ext(res.OutputPath) != ".pdf" {
		t.Fatalf("output should be .pdf, got %s", res.OutputPath)
	}
	assertValidPDFOutput(t, res.OutputPath)
}

// TestWordToPDFBinaries exercises the office-to-pdf path on a real DOCX. The
// fixture DOCX is itself generated in-code (LibreOffice converts a .txt to
// .docx first) so nothing binary is committed; that makes this test also
// cover officeToOffice's underlying conversion machinery.
func TestWordToPDFBinaries(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.txt")
	if err := os.WriteFile(src, []byte("Fyredocs DOCX fixture body text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(context.Background(), "libreoffice",
		"--headless", "--convert-to", "docx", "--outdir", dir, src).CombinedOutput()
	if err != nil {
		t.Fatalf("docx fixture generation failed: %v (output: %s)", err, out)
	}
	docx := filepath.Join(dir, "source.docx")
	if info, err := os.Stat(docx); err != nil || info.Size() == 0 {
		t.Fatalf("libreoffice produced no fixture docx: %v (output: %s)", err, out)
	}

	outDir := t.TempDir()
	res, err := ProcessFile(context.Background(), uuid.New(), "word-to-pdf", []string{docx}, nil, outDir, nil)
	if err != nil {
		t.Fatalf("ProcessFile word-to-pdf: %v", err)
	}
	assertValidPDFOutput(t, res.OutputPath)
}

// TestOfficeToPDFCorruptInputBinaries: garbage bytes with a .docx name must
// fail. LibreOffice exits 0 on an unreadable source while producing nothing,
// so the error surfaces from the missing-output rename — either way the
// caller must see a failure, never a phantom success with no file.
func TestOfficeToPDFCorruptInputBinaries(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "garbage.docx")
	if err := os.WriteFile(input, []byte("not a zip, not a docx \x00\x01\x02"), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()

	res, err := ProcessFile(context.Background(), uuid.New(), "word-to-pdf", []string{input}, nil, outDir, nil)
	if err == nil {
		t.Fatalf("expected error for corrupt input, got output %s", res.OutputPath)
	}
	if entries, _ := os.ReadDir(outDir); len(entries) != 0 {
		t.Errorf("corrupt input left %d files in the output dir", len(entries))
	}
}

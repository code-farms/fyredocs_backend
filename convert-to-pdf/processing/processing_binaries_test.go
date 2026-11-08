//go:build integration && binaries

// Real-binary exec-path tests for the convert-to-pdf worker. Unless a test
// starts the Supervisor itself, no unoserver daemon runs in the test container,
// so tryUnoconvert's pre-flight dial finds nothing listening,
// skips the unoconvert client, and officeToPDF takes its direct
// `libreoffice --headless` fallback — the exact degraded-daemon shape the
// fallback exists for. Run via
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
	"strings"
	"testing"
	"time"

	"fyredocs/shared/procexec"

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
	if err := api.ValidateFile(t.Context(), path, nil, nil); err != nil {
		t.Fatalf("output does not validate: %v", err)
	}
}

// TestHTMLToPDFBinaries drives the full ProcessFile dispatch for html-to-pdf:
// unoconvert is skipped at once (no daemon listening — see tryUnoconvert),
// then the direct LibreOffice fallback converts the generated HTML.
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

// sofficeProcesses returns the PIDs of running LibreOffice processes by scanning
// /proc, so the check needs no pgrep in the image. Zombies are excluded: a
// reaped-pending process holds no CPU, memory or files.
func sofficeProcesses(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatalf("read /proc: %v", err)
	}
	var pids []string
	for _, e := range entries {
		comm, err := os.ReadFile(filepath.Join("/proc", e.Name(), "comm"))
		if err != nil || !strings.HasPrefix(strings.TrimSpace(string(comm)), "soffice") {
			continue
		}
		status, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "status"))
		if strings.Contains(string(status), "State:\tZ") {
			continue
		}
		pids = append(pids, e.Name())
	}
	return pids
}

// TestCancelledLibreOfficeLeavesNoOrphansBinaries is the Stage 1 Q3 acceptance
// check (docs/developer/architecture/next-generation-document-engine/
// 20-implementation-roadmap.md): cancelling a job mid-conversion must stop the
// whole LibreOffice tree. Before shared/procexec, CommandContext killed only the
// `libreoffice` launcher and soffice.bin kept running after the job had failed.
func TestCancelledLibreOfficeLeavesNoOrphansBinaries(t *testing.T) {
	if before := sofficeProcesses(t); len(before) != 0 {
		t.Skipf("soffice already running (%v); cannot attribute orphans", before)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "source.txt")
	if err := os.WriteFile(src, []byte(strings.Repeat("Fyredocs orphan check line\n", 20000)), 0o644); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cmd := procexec.Command(ctx, "libreoffice", "--headless",
			"-env:UserInstallation=file://"+filepath.Join(dir, "profile"),
			"--convert-to", "pdf", "--outdir", t.TempDir(), src)
		if err := cmd.Start(); err != nil {
			t.Fatalf("start libreoffice: %v", err)
		}
		// Wait until soffice.bin is actually up, so the cancel lands mid-run.
		deadline := time.Now().Add(10 * time.Second)
		for len(sofficeProcesses(t)) == 0 && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		cancel()
		_ = cmd.Wait()

		deadline = time.Now().Add(3 * time.Second)
		for len(sofficeProcesses(t)) != 0 && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		if left := sofficeProcesses(t); len(left) != 0 {
			t.Fatalf("run %d: soffice still running after cancel: %v", i, left)
		}
	}
}

// TestSupervisorRealUnoserverBinaries runs the Go supervisor against real
// unoserver + LibreOffice: daemons become ready, a conversion goes through the
// warm path, a recycle brings a fresh daemon back, and Stop leaves no
// soffice.bin behind. Uses the default ports (2002…), which the package-level
// pool was built from at init.
func TestSupervisorRealUnoserverBinaries(t *testing.T) {
	if before := sofficeProcesses(t); len(before) != 0 {
		t.Skipf("soffice already running (%v)", before)
	}
	s := NewSupervisor()
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(s.Stop)
	if !s.WaitReady(context.Background(), 90*time.Second) {
		t.Fatal("daemon did not become ready")
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "source.txt")
	if err := os.WriteFile(src, []byte("Fyredocs supervisor check\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.pdf")
	start := time.Now()
	if !tryUnoconvert(context.Background(), src, out, "pdf") {
		t.Fatal("warm conversion through the supervised daemon failed")
	}
	assertValidPDFOutput(t, out)
	t.Logf("warm conversion: %v", time.Since(start))

	port := s.daemons[0].port
	if !s.Recycle(port) {
		t.Fatal("Recycle reported no running daemon")
	}
	if !s.WaitReady(context.Background(), 90*time.Second) {
		t.Fatal("recycled daemon did not come back")
	}

	s.Stop()
	deadline := time.Now().Add(5 * time.Second)
	for len(sofficeProcesses(t)) != 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if left := sofficeProcesses(t); len(left) != 0 {
		t.Fatalf("soffice still running after Stop: %v", left)
	}
}

// TestHindiWordToPDFEmbedsDevanagariFontBinaries is the Q6 acceptance check
// (docs/developer/architecture/next-generation-document-engine/
// 20-implementation-roadmap.md): Hindi in an Office document must come out of
// LibreOffice rendered with a real Devanagari font, not missing-glyph boxes.
// It asserts what survives PDF text extraction reliably: an embedded Noto Sans
// Devanagari font, and Devanagari characters in the text layer. Exact word
// matching is deliberately avoided — extraction can reorder vowel signs and
// conjuncts even when the page renders correctly.
func TestHindiWordToPDFEmbedsDevanagariFontBinaries(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "hindi.txt")
	text := "नमस्ते दुनिया। यह फायरडॉक्स का हिंदी परीक्षण है।\n"
	if err := os.WriteFile(src, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(context.Background(), "libreoffice", "--headless",
		"--infilter=Text (encoded):UTF8", "--convert-to", "docx", "--outdir", dir, src).CombinedOutput()
	if err != nil {
		t.Fatalf("docx fixture generation failed: %v (output: %s)", err, out)
	}
	docx := filepath.Join(dir, "hindi.docx")
	if info, err := os.Stat(docx); err != nil || info.Size() == 0 {
		t.Fatalf("libreoffice produced no fixture docx: %v (output: %s)", err, out)
	}

	outDir := t.TempDir()
	res, err := ProcessFile(context.Background(), uuid.New(), "word-to-pdf", []string{docx}, nil, outDir, nil)
	if err != nil {
		t.Fatalf("ProcessFile word-to-pdf: %v", err)
	}
	assertValidPDFOutput(t, res.OutputPath)

	fonts, err := exec.Command("pdffonts", res.OutputPath).CombinedOutput()
	if err != nil {
		t.Fatalf("pdffonts: %v (%s)", err, fonts)
	}
	embeddedDevanagari := false
	for _, line := range strings.Split(string(fonts), "\n") {
		// pdffonts columns: name type encoding emb sub uni object ID
		if strings.Contains(line, "NotoSansDevanagari") && strings.Contains(line, " yes ") {
			embeddedDevanagari = true
		}
	}
	if !embeddedDevanagari {
		t.Fatalf("PDF does not embed Noto Sans Devanagari; pdffonts:\n%s", fonts)
	}

	extracted, err := exec.Command("pdftotext", "-enc", "UTF-8", res.OutputPath, "-").Output()
	if err != nil {
		t.Fatalf("pdftotext: %v", err)
	}
	devanagari := 0
	for _, r := range string(extracted) {
		if r >= 0x0900 && r <= 0x097F {
			devanagari++
		}
	}
	if devanagari < 20 {
		t.Errorf("text layer has %d Devanagari characters, want >= 20; got %q", devanagari, extracted)
	}
}

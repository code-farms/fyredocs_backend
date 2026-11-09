//go:build integration && binaries

package processing

import (
	"archive/zip"
	"context"
	"fmt"
	"hash/crc32"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// Real-binary checks for the LibreOffice hardening in loprofile.go and the
// recycle-by-use policy in recycle_policy.go. They run in the test-binaries
// image (make test-binaries), which has LibreOffice and unoserver installed.

// registryFiles returns the installed LibreOffice registry (*.xcd) files.
func registryFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	for _, pattern := range []string{
		"/usr/lib/libreoffice/share/registry/*.xcd",
		"/usr/lib64/libreoffice/share/registry/*.xcd",
		"/opt/libreoffice*/share/registry/*.xcd",
	} {
		m, _ := filepath.Glob(pattern)
		files = append(files, m...)
	}
	if len(files) == 0 {
		t.Fatal("no LibreOffice registry (*.xcd) found; is LibreOffice installed in this image?")
	}
	return files
}

// schemaHas reports whether the component schema named component contains the
// group chain groups followed by prop of type typ, searching in document order.
// Nesting is approximated by order, which is enough to catch a misspelled or
// nonexistent key, the failure LibreOffice itself never reports.
func schemaHas(xcd, component string, groups []string, prop, typ string) bool {
	// Attribute order differs between .xcd files (main.xcd puts oor:package
	// first), so match the tag, not a fixed attribute sequence.
	loc := regexp.MustCompile(`<oor:component-schema[^>]*\boor:name="` + regexp.QuoteMeta(component) +
		`"[^>]*\boor:package="org\.openoffice\.Office"|<oor:component-schema[^>]*\boor:package="org\.openoffice\.Office"[^>]*\boor:name="` +
		regexp.QuoteMeta(component) + `"`).FindStringIndex(xcd)
	if loc == nil {
		return false
	}
	start := loc[0]
	section := xcd[start:]
	if end := strings.Index(section, "</oor:component-schema>"); end >= 0 {
		section = section[:end]
	}
	pos := 0
	for _, g := range groups {
		i := strings.Index(section[pos:], `oor:name="`+g+`"`)
		if i < 0 {
			return false
		}
		pos += i
	}
	i := strings.Index(section[pos:], `<prop oor:name="`+prop+`"`)
	if i < 0 {
		return false
	}
	tag := section[pos+i:]
	if end := strings.IndexByte(tag, '>'); end >= 0 {
		tag = tag[:end]
	}
	return strings.Contains(tag, `oor:type="`+typ+`"`)
}

// TestHardenedProfileKeysExistInSchemaBinaries: every key forced by
// hardenedSettings exists, with that type, in the LibreOffice installed in the
// image. LibreOffice ignores unknown keys silently, so without this a typo
// would leave a security setting at its default.
func TestHardenedProfileKeysExistInSchemaBinaries(t *testing.T) {
	var all strings.Builder
	for _, f := range registryFiles(t) {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		all.Write(b)
	}
	xcd := all.String()
	for _, s := range hardenedSettings {
		parts := strings.Split(strings.TrimPrefix(s.Path, "/org.openoffice.Office."), "/")
		if !schemaHas(xcd, parts[0], parts[1:], s.Prop, s.Type) {
			t.Errorf("%s/%s (%s) is not in the installed LibreOffice schema", s.Path, s.Prop, s.Type)
		}
	}
}

// macroODT writes an ODT whose document-open event runs a Basic macro that
// creates sentinel. If LibreOffice runs document macros during conversion, the
// sentinel appears.
func macroODT(t *testing.T, path, sentinel string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	// mimetype must be the first entry, stored uncompressed, with its CRC and
	// sizes in the local header. zip.CreateHeader would stream it with a
	// trailing data descriptor, and LibreOffice then refuses the package.
	mt := []byte("application/vnd.oasis.opendocument.text")
	w, err := zw.CreateRaw(&zip.FileHeader{
		Name: "mimetype", Method: zip.Store, CRC32: crc32.ChecksumIEEE(mt),
		CompressedSize64: uint64(len(mt)), UncompressedSize64: uint64(len(mt)),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write(mt)
	files := map[string]string{
		"META-INF/manifest.xml": `<?xml version="1.0" encoding="UTF-8"?>
<manifest:manifest xmlns:manifest="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0" manifest:version="1.2">
 <manifest:file-entry manifest:full-path="/" manifest:version="1.2" manifest:media-type="application/vnd.oasis.opendocument.text"/>
 <manifest:file-entry manifest:full-path="content.xml" manifest:media-type="text/xml"/>
 <manifest:file-entry manifest:full-path="Basic/script-lc.xml" manifest:media-type="text/xml"/>
 <manifest:file-entry manifest:full-path="Basic/Standard/script-lb.xml" manifest:media-type="text/xml"/>
 <manifest:file-entry manifest:full-path="Basic/Standard/Module1.xml" manifest:media-type="text/xml"/>
</manifest:manifest>`,
		"content.xml": `<?xml version="1.0" encoding="UTF-8"?>
<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" xmlns:script="urn:oasis:names:tc:opendocument:xmlns:script:1.0" xmlns:xlink="http://www.w3.org/1999/xlink" xmlns:dom="http://www.w3.org/2001/xml-events" office:version="1.2">
 <office:scripts><office:event-listeners>
  <script:event-listener script:language="ooo:script" script:event-name="dom:load" xlink:href="vnd.sun.star.script:Standard.Module1.Main?language=Basic&amp;location=document" xlink:type="simple"/>
 </office:event-listeners></office:scripts>
 <office:body><office:text><text:p>Fyredocs macro sentinel fixture</text:p></office:text></office:body>
</office:document-content>`,
		"Basic/script-lc.xml": `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE library:libraries PUBLIC "-//OpenOffice.org//DTD OfficeDocument 1.0//EN" "libraries.dtd">
<library:libraries xmlns:library="http://openoffice.org/2000/library" xmlns:xlink="http://www.w3.org/1999/xlink">
 <library:library library:name="Standard" library:link="false"/>
</library:libraries>`,
		"Basic/Standard/script-lb.xml": `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE library:library PUBLIC "-//OpenOffice.org//DTD OfficeDocument 1.0//EN" "library.dtd">
<library:library xmlns:library="http://openoffice.org/2000/library" library:name="Standard" library:readonly="false" library:passwordprotected="false">
 <library:element library:name="Module1"/>
</library:library>`,
		"Basic/Standard/Module1.xml": fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE script:module PUBLIC "-//OpenOffice.org//DTD OfficeDocument 1.0//EN" "module.dtd">
<script:module xmlns:script="http://openoffice.org/2000/script" script:name="Module1" script:language="StarBasic">Sub Main
  Dim n As Integer
  n = FreeFile
  Open &quot;%s&quot; For Output As #n
  Print #n, &quot;macro ran&quot;
  Close #n
End Sub
</script:module>`, sentinel),
	}
	// Fixed order: LibreOffice refuses the package ("source file could not be
	// loaded") when the manifest comes after the Basic files, which random map
	// iteration produced (measured 2026-11-09).
	for _, name := range []string{"META-INF/manifest.xml", "content.xml", "Basic/script-lc.xml",
		"Basic/Standard/script-lb.xml", "Basic/Standard/Module1.xml"} {
		body := files[name]
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// permissiveProfile writes a profile that allows every macro, for the control
// run: it shows whether headless conversion fires the open event at all.
func permissiveProfile(t *testing.T, dir string) string {
	t.Helper()
	user := filepath.Join(dir, "user")
	if err := os.MkdirAll(user, 0o700); err != nil {
		t.Fatal(err)
	}
	reg := `<?xml version="1.0" encoding="UTF-8"?>
<oor:items xmlns:oor="http://openoffice.org/2001/registry" xmlns:xs="http://www.w3.org/2001/XMLSchema">
<item oor:path="/org.openoffice.Office.Common/Security/Scripting"><prop oor:name="DisableMacrosExecution" oor:op="fuse"><value>false</value></prop></item>
<item oor:path="/org.openoffice.Office.Common/Security/Scripting"><prop oor:name="MacroSecurityLevel" oor:op="fuse"><value>0</value></prop></item>
</oor:items>`
	if err := os.WriteFile(filepath.Join(user, "registrymodifications.xcu"), []byte(reg), 0o600); err != nil {
		t.Fatal(err)
	}
	return "-env:UserInstallation=file://" + dir
}

// convertWithProfile runs a cold LibreOffice conversion of src to PDF in outDir
// with the given -env:UserInstallation argument.
func convertWithProfile(t *testing.T, profileArg, src, outDir string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "libreoffice", profileArg, "--headless",
		"--convert-to", "pdf", "--outdir", outDir, src).CombinedOutput()
	if err != nil {
		t.Fatalf("libreoffice: %v (output: %s)", err, out)
	}
}

// TestMacroDoesNotRunWithHardenedProfileBinaries is the sentinel check from
// 15-security-and-reliability.md §8: a document whose open event runs a macro
// is converted with the hardened fallback profile and through a hardened pooled
// daemon, and the macro must not run on either path. The permissive control
// run is logged so the strength of the proof is known: if the event never fires
// even when allowed, the hardened result shows less than it seems.
func TestMacroDoesNotRunWithHardenedProfileBinaries(t *testing.T) {
	dir := t.TempDir()

	ctlSentinel := filepath.Join(dir, "control-sentinel")
	ctlSrc := filepath.Join(dir, "control.odt")
	macroODT(t, ctlSrc, ctlSentinel)
	ctlOut := filepath.Join(dir, "control-out")
	_ = os.MkdirAll(ctlOut, 0o755)
	convertWithProfile(t, permissiveProfile(t, filepath.Join(dir, "permissive")), ctlSrc, ctlOut)
	// The fixture must load, or every result below is vacuous (LibreOffice
	// exits 0 even when it cannot load the source).
	assertValidPDFOutput(t, filepath.Join(ctlOut, "control.pdf"))
	_, ctlErr := os.Stat(ctlSentinel)
	controlFired := ctlErr == nil
	t.Logf("control (macros allowed): open-event macro ran during headless conversion = %v", controlFired)

	coldSentinel := filepath.Join(dir, "cold-sentinel")
	coldSrc := filepath.Join(dir, "cold.odt")
	macroODT(t, coldSrc, coldSentinel)
	coldOut := filepath.Join(dir, "cold-out")
	_ = os.MkdirAll(coldOut, 0o755)
	arg, cleanup, err := newFallbackProfile(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	convertWithProfile(t, arg, coldSrc, coldOut)
	if _, err := os.Stat(coldSentinel); err == nil {
		t.Error("macro ran in the cold fallback despite the hardened profile")
	}
	assertValidPDFOutput(t, filepath.Join(coldOut, "cold.pdf"))

	if before := sofficeProcesses(t); len(before) != 0 {
		t.Skipf("soffice already running (%v); daemon path not checked", before)
	}
	s := NewSupervisor()
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(s.Stop)
	if !s.WaitReady(context.Background(), 90*time.Second) {
		t.Fatal("daemon did not become ready")
	}
	warmSentinel := filepath.Join(dir, "warm-sentinel")
	warmSrc := filepath.Join(dir, "warm.odt")
	macroODT(t, warmSrc, warmSentinel)
	warmOut := filepath.Join(dir, "warm.pdf")
	if !tryUnoconvert(context.Background(), warmSrc, warmOut, "pdf") {
		t.Fatal("warm conversion through the hardened daemon failed")
	}
	if _, err := os.Stat(warmSentinel); err == nil {
		t.Error("macro ran in a pooled daemon despite the hardened profile")
	}
	assertValidPDFOutput(t, warmOut)
	if !controlFired {
		t.Log("NOTE: the control did not fire either; the hardened result proves the settings are applied, not that they were needed for this fixture")
	}
}

// TestConcurrentColdFallbacksBinaries: two cold LibreOffice conversions at the
// same time both succeed, ten rounds in a row. Shared profiles contended on one
// lock (D6); fresh per-call profiles crashed one process in about 1 round in 10
// during LibreOffice's first-start initialisation, which the initialised
// template (fallbackTemplate) removes. Ten rounds make that rate visible.
func TestConcurrentColdFallbacksBinaries(t *testing.T) {
	saved := activeSupervisor.Swap(nil)
	t.Cleanup(func() { activeSupervisor.Store(saved) })
	if fallbackTemplate() == "" {
		t.Fatal("the initialised template profile could not be built")
	}
	if v := readRegistry(t, fallbackTemplate())["/org.openoffice.Office.Common/Security/Scripting|DisableMacrosExecution"]; v != "true" {
		t.Errorf("template lost the hardened settings after initialisation: DisableMacrosExecution = %q", v)
	}
	for round := 0; round < 10; round++ {
		dir := t.TempDir()
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := range errs {
			src := filepath.Join(dir, fmt.Sprintf("doc%d.txt", i))
			if err := os.WriteFile(src, []byte(fmt.Sprintf("concurrent fallback %d\n", i)), 0o644); err != nil {
				t.Fatal(err)
			}
			outDir := filepath.Join(dir, fmt.Sprintf("job%d", i), "out")
			_ = os.MkdirAll(outDir, 0o755)
			wg.Add(1)
			go func(i int, src, out string) {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
				defer cancel()
				errs[i] = officeToPDF(ctx, src, out, "txt")
			}(i, src, filepath.Join(outDir, "out.pdf"))
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d, fallback %d: %v", round, i, err)
			}
			assertValidPDFOutput(t, filepath.Join(dir, fmt.Sprintf("job%d", i), "out", "out.pdf"))
		}
	}
}

// TestRecycleAfterMaxConversionsBinaries: with a limit of 3, the 4th job on a
// single-daemon pool is served by a fresh daemon and none fall back cold.
func TestRecycleAfterMaxConversionsBinaries(t *testing.T) {
	if before := sofficeProcesses(t); len(before) != 0 {
		t.Skipf("soffice already running (%v)", before)
	}
	t.Setenv("UNOSERVER_INSTANCES", "1")
	t.Setenv("UNOSERVER_MAX_CONVERSIONS", "3")
	s := NewSupervisor()
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(s.Stop)
	if !s.WaitReady(context.Background(), 90*time.Second) {
		t.Fatal("daemon did not become ready")
	}
	d := s.daemons[0]
	withTestPool(t, d.port)

	dir := t.TempDir()
	pgidOf := func() int { d.mu.Lock(); defer d.mu.Unlock(); return d.pgid }
	first := pgidOf()
	var lastPgid int
	for i := 1; i <= 4; i++ {
		src := filepath.Join(dir, fmt.Sprintf("doc%d.txt", i))
		if err := os.WriteFile(src, []byte(fmt.Sprintf("recycle check %d\n", i)), 0o644); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		ok := tryUnoconvert(ctx, src, filepath.Join(dir, fmt.Sprintf("out%d.pdf", i)), "pdf")
		cancel()
		if !ok {
			t.Fatalf("conversion %d fell back to cold LibreOffice", i)
		}
		lastPgid = pgidOf()
	}
	if lastPgid == first || lastPgid == 0 {
		t.Errorf("4th conversion ran on pgid %d, want a fresh daemon (first was %d)", lastPgid, first)
	}
}

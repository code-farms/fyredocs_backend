package processing

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestPdfaDefinitionEmbedsProfileAndOutputIntent guards the pieces PDF/A-2b
// needs: the ICC stream, an OutputIntent using it, and the Catalog entry.
// Missing any of these is exactly the defect this file was written to fix.
func TestPdfaDefinitionEmbedsProfileAndOutputIntent(t *testing.T) {
	def := pdfaDefinition("/usr/share/icc/srgb.icc")
	for _, want := range []string{
		"/ICCProfile (/usr/share/icc/srgb.icc) def",
		"<< /N 3 >>",
		"/S /GTS_PDFA1",
		"/DestOutputProfile {icc_PDFA}",
		"/OutputIntents [ {OutputIntent_PDFA} ]",
	} {
		if !strings.Contains(def, want) {
			t.Errorf("definition lacks %q:\n%s", want, def)
		}
	}
}

// TestPdfaDefinitionEscapesPath: a path with PostScript string metacharacters
// must not break out of the (...) literal.
func TestPdfaDefinitionEscapesPath(t *testing.T) {
	def := pdfaDefinition(`/opt/icc (v2)\srgb.icc`)
	if !strings.Contains(def, `/ICCProfile (/opt/icc \(v2\)\\srgb.icc) def`) {
		t.Errorf("path not escaped:\n%s", def)
	}
}

// TestPdfaArgs pins the flags whose absence or wrong form broke PDF/A output:
// the integer -dPDFACompatibilityPolicy (the -s string form aborted Ghostscript
// 10.08), RGB colour conversion, read permission for the profile, and the
// definition file placed before the input.
func TestPdfaArgs(t *testing.T) {
	args := pdfaArgs("/tmp/def.ps", "/icc/srgb.icc", "/in.pdf", "/out.pdf")
	for _, want := range []string{
		"-dPDFA=2", "-dSAFER", "-dPDFACompatibilityPolicy=1",
		"-sColorConversionStrategy=RGB", "--permit-file-read=/icc/srgb.icc", "-sOutputFile=/out.pdf",
	} {
		if !slices.Contains(args, want) {
			t.Errorf("args lack %q: %v", want, args)
		}
	}
	if slices.Contains(args, "-sPDFACompatibilityPolicy=1") {
		t.Error("string-typed -sPDFACompatibilityPolicy is back")
	}
	def, in := slices.Index(args, "/tmp/def.ps"), slices.Index(args, "/in.pdf")
	if def < 0 || in < 0 || def > in {
		t.Errorf("definition file must come before the input: %v", args)
	}
}

// TestSrgbProfilePath covers discovery order: env override, then candidates,
// then a clear error instead of a non-conformant file.
func TestSrgbProfilePath(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "v10", "iccprofiles", "srgb.icc")
	if err := os.MkdirAll(filepath.Dir(profile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profile, []byte("icc"), 0o644); err != nil {
		t.Fatal(err)
	}
	saved := srgbProfileCandidates
	t.Cleanup(func() { srgbProfileCandidates = saved })

	t.Run("glob candidate", func(t *testing.T) {
		t.Setenv(pdfaICCEnv, "")
		srgbProfileCandidates = []string{filepath.Join(dir, "missing.icc"), filepath.Join(dir, "*", "iccprofiles", "srgb.icc")}
		if got, err := srgbProfilePath(); err != nil || got != profile {
			t.Errorf("srgbProfilePath() = %q, %v; want %q", got, err, profile)
		}
	})
	t.Run("env override", func(t *testing.T) {
		t.Setenv(pdfaICCEnv, profile)
		srgbProfileCandidates = nil
		if got, err := srgbProfilePath(); err != nil || got != profile {
			t.Errorf("srgbProfilePath() = %q, %v; want %q", got, err, profile)
		}
	})
	t.Run("env override missing", func(t *testing.T) {
		t.Setenv(pdfaICCEnv, filepath.Join(dir, "nope.icc"))
		if _, err := srgbProfilePath(); err == nil {
			t.Error("missing PDFA_ICC_PROFILE file accepted")
		}
	})
	t.Run("none found", func(t *testing.T) {
		t.Setenv(pdfaICCEnv, "")
		srgbProfileCandidates = []string{filepath.Join(dir, "missing.icc")}
		if _, err := srgbProfilePath(); !errors.Is(err, errNoSRGBProfile) {
			t.Errorf("err = %v, want errNoSRGBProfile", err)
		}
	})
}

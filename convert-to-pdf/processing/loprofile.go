package processing

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"fyredocs/shared/procexec"
)

// LibreOffice profile hardening. Every LibreOffice this worker runs (each pooled
// unoserver daemon and every cold-fallback process) gets a profile whose
// registrymodifications.xcu turns off what untrusted Office documents could use
// against us. LibreOffice's defaults leave macros at security level 2, import VBA
// into Office Basic (so an ODF output can carry the converted macro), and update
// links "on request". Headless conversion is believed not to fire document
// macros at level 2, but that rests on forum evidence; these settings make it a
// configured property instead (docs/developer/architecture/
// next-generation-document-engine/15-security-and-reliability.md §5.3, §8).
//
// LibreOffice silently ignores a key it does not know, so a typo here would
// leave a setting at its default with no error. The binaries test
// TestHardenedProfileKeysExistInSchema checks every key against the registry
// schema installed in the image.
//
// NOTE: duplicated by design in convert-from-pdf/processing/loprofile.go
// (CLAUDE.md §1, no shared business logic); keep the two tables identical.

// profileSetting is one registry value: an item path, a prop name, its schema
// type and the value to force.
type profileSetting struct {
	Path  string
	Prop  string
	Type  string
	Value string
}

// hardenedSettings is the single source of the forced values. Paths and value
// meanings are from LibreOffice's officecfg schemas (Common.xcs, Writer.xcs,
// Calc.xcs, Impress.xcs). Link-update values differ by application: Writer
// 2 = never, Calc 1 = never. DisableActiveContent is deliberately absent: it
// also stops OLE objects rendering, which would change PDF output.
var hardenedSettings = []profileSetting{
	// Disables Basic, BeanShell, JavaScript and Python macros outright; the
	// level below is a second line in case a future version changes that.
	{"/org.openoffice.Office.Common/Security/Scripting", "DisableMacrosExecution", "xs:boolean", "true"},
	{"/org.openoffice.Office.Common/Security/Scripting", "MacroSecurityLevel", "xs:int", "3"},
	{"/org.openoffice.Office.Common/Security/Scripting", "BlockUntrustedRefererLinks", "xs:boolean", "true"},
	{"/org.openoffice.Office.Writer/Content/Update", "Link", "xs:int", "2"},
	{"/org.openoffice.Office.Calc/Content/Update", "Link", "xs:int", "1"},
	{"/org.openoffice.Office.Writer/Filter/Import/VBA", "Load", "xs:boolean", "false"},
	{"/org.openoffice.Office.Writer/Filter/Import/VBA", "Executable", "xs:boolean", "false"},
	{"/org.openoffice.Office.Writer/Filter/Import/VBA", "Save", "xs:boolean", "false"},
	{"/org.openoffice.Office.Calc/Filter/Import/VBA", "Load", "xs:boolean", "false"},
	{"/org.openoffice.Office.Calc/Filter/Import/VBA", "Executable", "xs:boolean", "false"},
	{"/org.openoffice.Office.Calc/Filter/Import/VBA", "Save", "xs:boolean", "false"},
	{"/org.openoffice.Office.Calc/Filter/Import/VBA", "UseExport", "xs:boolean", "false"},
	{"/org.openoffice.Office.Impress/Filter/Import/VBA", "Load", "xs:boolean", "false"},
	{"/org.openoffice.Office.Impress/Filter/Import/VBA", "Save", "xs:boolean", "false"},
}

// hardenedRegistry renders hardenedSettings as a registrymodifications.xcu.
// oor:op="fuse" is what LibreOffice itself writes for a changed value.
func hardenedRegistry() []byte {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<oor:items xmlns:oor="http://openoffice.org/2001/registry" ` +
		`xmlns:xs="http://www.w3.org/2001/XMLSchema" ` +
		`xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` + "\n")
	for _, s := range hardenedSettings {
		fmt.Fprintf(&b, `<item oor:path="%s"><prop oor:name="%s" oor:op="fuse" oor:type="%s"><value>%s</value></prop></item>`+"\n",
			xmlEscape(s.Path), xmlEscape(s.Prop), xmlEscape(s.Type), xmlEscape(s.Value))
	}
	b.WriteString("</oor:items>\n")
	return b.Bytes()
}

// xmlEscape escapes s for use in XML text or a double-quoted attribute.
func xmlEscape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// writeHardenedProfile writes the hardened registrymodifications.xcu into the
// LibreOffice profile at dir (the directory passed as --user-installation or
// -env:UserInstallation; LibreOffice keeps its settings in dir/user). It
// replaces any existing file: LibreOffice rewrites that file on exit, so a
// profile that ran before is never trusted to still hold these values. The write
// goes to a temporary file and is renamed into place, so a crash cannot leave a
// truncated registry. An empty dir is an error rather than a write into the
// current directory.
func writeHardenedProfile(dir string) error {
	if dir == "" {
		return errors.New("libreoffice profile: empty directory")
	}
	userDir := filepath.Join(dir, "user")
	if err := os.MkdirAll(userDir, 0o700); err != nil {
		return fmt.Errorf("libreoffice profile: %w", err)
	}
	tmp, err := os.CreateTemp(userDir, ".registrymodifications-*.xcu")
	if err != nil {
		return fmt.Errorf("libreoffice profile: %w", err)
	}
	_, werr := tmp.Write(hardenedRegistry())
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("libreoffice profile: write: %w", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(userDir, "registrymodifications.xcu")); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("libreoffice profile: %w", err)
	}
	return nil
}

// newFallbackProfile creates a fresh hardened LibreOffice profile under baseDir
// for one cold-fallback conversion and returns the -env:UserInstallation
// argument for it plus a cleanup func. Without its own profile every fallback
// shares ~/.config/libreoffice, so two concurrent fallbacks contend on the
// profile lock and the second can hang or fail (D6 in
// docs/developer/architecture/next-generation-document-engine/
// 01-existing-system-audit.md). baseDir should be the job's scratch directory so
// the worker's scratch cleanup also removes the profile if cleanup is skipped.
func newFallbackProfile(baseDir string) (arg string, cleanup func(), err error) {
	dir, err := os.MkdirTemp(baseDir, "lo-profile-")
	if err != nil {
		return "", nil, fmt.Errorf("fallback profile: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	// Start from the initialised template when there is one, then force the
	// hardened registry again: the copy is never trusted to still hold it.
	if tmpl := fallbackTemplate(); tmpl != "" {
		if err := copyTree(tmpl, dir); err != nil {
			slog.Warn("libreoffice profile: copying the template failed; using a fresh profile", "error", err)
		}
	}
	if err := writeHardenedProfile(dir); err != nil {
		cleanup()
		return "", nil, err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("fallback profile: %w", err)
	}
	return "-env:UserInstallation=" + (&url.URL{Scheme: "file", Path: abs}).String(), cleanup, nil
}

// fallbackTemplateWait bounds the one-time LibreOffice start that initialises
// the template profile.
const fallbackTemplateWait = 2 * time.Minute

var (
	templateOnce sync.Once
	// templateDir is the initialised hardened template profile, or "" if it
	// could not be built (each fallback then starts from a fresh profile).
	templateDir string
)

// fallbackTemplate returns a hardened LibreOffice profile that LibreOffice has
// already initialised, built once per process on first use (concurrent first
// callers wait for it). Starting LibreOffice concurrently on brand-new profiles
// crashed one of the two processes in about 1 run in 10 ("terminate called
// after throwing an instance of WrappedTargetRuntimeException"), while copies
// of an initialised profile failed 0 times in 20 (measured 2026-11-09 in the
// test-binaries image). It is built with `--terminate_after_init`, which runs
// LibreOffice's first-start initialisation and exits without converting.
// Returns "" when LibreOffice is missing or the start fails; the caller then
// uses a fresh profile, which still works most of the time.
func fallbackTemplate() string {
	templateOnce.Do(func() {
		dir := filepath.Join(os.TempDir(), "lo-profile-template")
		_ = os.RemoveAll(dir)
		if err := writeHardenedProfile(dir); err != nil {
			slog.Warn("libreoffice template profile: write failed; fallbacks use fresh profiles", "error", err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), fallbackTemplateWait)
		defer cancel()
		u := url.URL{Scheme: "file", Path: dir}
		out, err := procexec.Command(ctx, "libreoffice", "-env:UserInstallation="+u.String(),
			"--headless", "--terminate_after_init").CombinedOutput()
		if err != nil {
			slog.Warn("libreoffice template profile: initialisation failed; fallbacks use fresh profiles",
				"error", err, "output", string(out))
			_ = os.RemoveAll(dir)
			return
		}
		templateDir = dir
	})
	return templateDir
}

// copyTree copies the directory tree at src into dst (which must exist),
// keeping file modes. Symlinks and other special files are skipped: a
// LibreOffice profile holds only directories and regular files.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || rel == "." {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case info.Mode().IsRegular():
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(target, data, info.Mode().Perm())
		default:
			return nil
		}
	})
}

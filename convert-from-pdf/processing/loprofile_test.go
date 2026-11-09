package processing

import (
	"encoding/xml"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// registryItem mirrors one <item> of registrymodifications.xcu for parsing.
type registryItem struct {
	Path string `xml:"path,attr"`
	Prop struct {
		Name  string `xml:"name,attr"`
		Op    string `xml:"op,attr"`
		Value string `xml:"value"`
	} `xml:"prop"`
}

// readRegistry parses the profile's registry and returns path|prop → value.
func readRegistry(t *testing.T, dir string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "user", "registrymodifications.xcu"))
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	var doc struct {
		Items []registryItem `xml:"item"`
	}
	if err := xml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("registry is not valid XML: %v", err)
	}
	got := map[string]string{}
	for _, it := range doc.Items {
		if it.Prop.Op != "fuse" {
			t.Errorf("%s/%s: op %q, want fuse", it.Path, it.Prop.Name, it.Prop.Op)
		}
		got[it.Path+"|"+it.Prop.Name] = it.Prop.Value
	}
	return got
}

func TestWriteHardenedProfileForcesEverySetting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "profile")
	if err := writeHardenedProfile(dir); err != nil {
		t.Fatal(err)
	}
	got := readRegistry(t, dir)
	for _, s := range hardenedSettings {
		if v := got[s.Path+"|"+s.Prop]; v != s.Value {
			t.Errorf("%s/%s = %q, want %q", s.Path, s.Prop, v, s.Value)
		}
	}
	if len(got) != len(hardenedSettings) {
		t.Errorf("registry has %d items, want %d", len(got), len(hardenedSettings))
	}
}

// TestHardenedSettingsCoverTheThreats pins the security intent, so a later edit
// cannot quietly drop a setting: macros off, links never updated in Writer and
// Calc (their "never" values differ), VBA not imported anywhere.
func TestHardenedSettingsCoverTheThreats(t *testing.T) {
	want := map[string]string{
		"/org.openoffice.Office.Common/Security/Scripting|DisableMacrosExecution": "true",
		"/org.openoffice.Office.Common/Security/Scripting|MacroSecurityLevel":     "3",
		"/org.openoffice.Office.Writer/Content/Update|Link":                       "2",
		"/org.openoffice.Office.Calc/Content/Update|Link":                         "1",
		"/org.openoffice.Office.Writer/Filter/Import/VBA|Load":                    "false",
		"/org.openoffice.Office.Calc/Filter/Import/VBA|Load":                      "false",
		"/org.openoffice.Office.Impress/Filter/Import/VBA|Load":                   "false",
	}
	have := map[string]string{}
	for _, s := range hardenedSettings {
		have[s.Path+"|"+s.Prop] = s.Value
	}
	for k, v := range want {
		if have[k] != v {
			t.Errorf("%s = %q, want %q", k, have[k], v)
		}
	}
}

func TestWriteHardenedProfileReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "user")
	if err := os.MkdirAll(user, 0o700); err != nil {
		t.Fatal(err)
	}
	// What LibreOffice might leave behind: macros re-enabled.
	tampered := `<?xml version="1.0"?><oor:items xmlns:oor="http://openoffice.org/2001/registry">` +
		`<item oor:path="/org.openoffice.Office.Common/Security/Scripting"><prop oor:name="DisableMacrosExecution" oor:op="fuse"><value>false</value></prop></item></oor:items>`
	if err := os.WriteFile(filepath.Join(user, "registrymodifications.xcu"), []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeHardenedProfile(dir); err != nil {
		t.Fatal(err)
	}
	if v := readRegistry(t, dir)["/org.openoffice.Office.Common/Security/Scripting|DisableMacrosExecution"]; v != "true" {
		t.Errorf("DisableMacrosExecution = %q after rewrite, want true", v)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(user, ".registrymodifications-*")); len(leftovers) != 0 {
		t.Errorf("temporary files left behind: %v", leftovers)
	}
}

func TestWriteHardenedProfileRejectsEmptyDir(t *testing.T) {
	if err := writeHardenedProfile(""); err == nil {
		t.Error("empty dir must be an error, not a write into the working directory")
	}
}

func TestNewFallbackProfile(t *testing.T) {
	base := t.TempDir()
	arg, cleanup, err := newFallbackProfile(base)
	if err != nil {
		t.Fatal(err)
	}
	const prefix = "-env:UserInstallation="
	if !strings.HasPrefix(arg, prefix) {
		t.Fatalf("arg = %q, want %s<file URL>", arg, prefix)
	}
	u, err := url.Parse(strings.TrimPrefix(arg, prefix))
	if err != nil || u.Scheme != "file" {
		t.Fatalf("arg %q does not carry a file URL: %v", arg, err)
	}
	if !strings.HasPrefix(u.Path, base) {
		t.Errorf("profile %s is not inside the scratch dir %s", u.Path, base)
	}
	if v := readRegistry(t, u.Path)["/org.openoffice.Office.Common/Security/Scripting|DisableMacrosExecution"]; v != "true" {
		t.Errorf("fallback profile is not hardened: DisableMacrosExecution = %q", v)
	}
	cleanup()
	if _, err := os.Stat(u.Path); !os.IsNotExist(err) {
		t.Errorf("cleanup left %s behind", u.Path)
	}
}

// TestNewFallbackProfileIsPerCall: two concurrent fallbacks must never share a
// profile (D6).
func TestNewFallbackProfileIsPerCall(t *testing.T) {
	base := t.TempDir()
	a, ca, err := newFallbackProfile(base)
	if err != nil {
		t.Fatal(err)
	}
	defer ca()
	b, cb, err := newFallbackProfile(base)
	if err != nil {
		t.Fatal(err)
	}
	defer cb()
	if a == b {
		t.Errorf("two calls returned the same profile %q", a)
	}
}

func TestCopyTree(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "user", "basic"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "user", "basic", "x.xml"), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if err := copyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "user", "basic", "x.xml"))
	if err != nil || string(got) != "payload" {
		t.Fatalf("copied file = %q, %v", got, err)
	}
	info, _ := os.Stat(filepath.Join(dst, "user", "basic", "x.xml"))
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

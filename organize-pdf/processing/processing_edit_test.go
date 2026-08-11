// Tests for the edit/sign path. The invariants under guard: data-URL images
// land on disk with an extension that matches their mime prefix (a JPEG named
// .png used to ship), and signPDF stamps the requested page without corrupting
// the document — pure pdfcpu, so the round-trip runs everywhere.
package processing

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

func TestDecodeDataURLImage(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString([]byte("img-bytes"))
	tests := []struct {
		name      string
		dataURL   string
		allowWebp bool
		wantExt   string
		wantErr   bool
	}{
		{"png", "data:image/png;base64," + payload, false, ".png", false},
		{"jpeg maps to .jpg", "data:image/jpeg;base64," + payload, false, ".jpg", false},
		{"webp allowed", "data:image/webp;base64," + payload, true, ".webp", false},
		{"webp disallowed falls back to png", "data:image/webp;base64," + payload, false, ".png", false},
		{"no comma", "data:image/png;base64", false, "", true},
		{"bad base64", "data:image/png;base64,%%%", false, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path, err := decodeDataURLImage(tt.dataURL, dir, "sig", tt.allowWebp)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := filepath.Ext(path); got != tt.wantExt {
				t.Errorf("ext = %q, want %q", got, tt.wantExt)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "img-bytes" {
				t.Errorf("file content mismatch: %v %q", err, data)
			}
		})
	}
}

// TestSignPDFRoundTrip builds a 2-page PDF from images, signs page 1, and
// asserts the output still parses with both pages intact. Pure Go (pdfcpu),
// no external tools — runs everywhere.
func TestSignPDFRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p1 := filepath.Join(dir, "p1.png")
	p2 := filepath.Join(dir, "p2.png")
	writeTestPNG(t, p1, 200, 260)
	writeTestPNG(t, p2, 200, 260)
	input := filepath.Join(dir, "in.pdf")
	if err := api.ImportImagesFile([]string{p1, p2}, input, nil, nil); err != nil {
		t.Fatalf("build input pdf: %v", err)
	}

	sig := filepath.Join(dir, "sig.png")
	writeTestPNG(t, sig, 120, 40)
	sigBytes, err := os.ReadFile(sig)
	if err != nil {
		t.Fatal(err)
	}
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(sigBytes)

	output := filepath.Join(dir, "out.pdf")
	options := map[string]interface{}{
		"signatureData": dataURL,
		"position":      "br",
		"page":          "1",
	}
	if err := signPDF(input, output, options); err != nil {
		t.Fatalf("signPDF: %v", err)
	}

	ctx, err := api.ReadContextFile(output)
	if err != nil {
		t.Fatalf("output does not parse: %v", err)
	}
	if ctx.PageCount != 2 {
		t.Errorf("page count = %d, want 2", ctx.PageCount)
	}
	if fi, err := os.Stat(output); err != nil || fi.Size() == 0 {
		t.Fatalf("output missing or empty: %v", err)
	}
}

// TestSignPDFRejectsBadInput pins the error paths that must fail before any
// pdfcpu work happens.
func TestSignPDFRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.pdf")
	if err := signPDF(filepath.Join(dir, "in.pdf"), out, map[string]interface{}{}); err == nil ||
		!strings.Contains(err.Error(), "missing signature data") {
		t.Errorf("missing signatureData not rejected: %v", err)
	}
	if err := signPDF(filepath.Join(dir, "in.pdf"), out, map[string]interface{}{
		"signatureData": "not-a-data-url",
	}); err == nil || !strings.Contains(fmt.Sprint(err), "invalid image data format") {
		t.Errorf("malformed data URL not rejected: %v", err)
	}
}

// TestMain mirrors main.go's DisableConfigDir: without it, the first pdfcpu
// call from the test runner loads — and on first use writes — a config.yml
// under os.UserConfigDir(), making encryption defaults depend on the host.
func TestMain(m *testing.M) {
	api.DisableConfigDir()
	os.Exit(m.Run())
}

func boolPtr(v bool) *bool { return &v }

// buildProtectInput renders a 1-page PDF from a PNG — the same pure-pdfcpu
// fixture recipe as TestSignPDFRoundTrip, so it runs everywhere.
func buildProtectInput(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	png := filepath.Join(dir, "p1.png")
	writeTestPNG(t, png, 200, 260)
	input := filepath.Join(dir, "input.pdf")
	if err := api.ImportImagesFile([]string{png}, input, nil, nil); err != nil {
		t.Fatalf("build input pdf: %v", err)
	}
	return input
}

// readEncrypted opens an encrypted output with the supplied passwords —
// api.ReadContextFile always uses a password-less default configuration.
func readEncrypted(t *testing.T, path, userPW, ownerPW string) (*model.Context, error) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	conf := model.NewDefaultConfiguration()
	conf.UserPW = userPW
	conf.OwnerPW = ownerPW
	ctx, err := api.ReadContext(f, conf)
	if err != nil {
		return nil, err
	}
	// Validation populates PageCount (a bare read leaves it 0).
	if err := api.ValidateContext(ctx); err != nil {
		return nil, err
	}
	return ctx, nil
}

func TestParseProtectOptions(t *testing.T) {
	tests := []struct {
		name    string
		options map[string]interface{}
		want    protectSettings
		wantErr string
	}{
		{
			"legacy password sets both",
			map[string]interface{}{"password": "s3cret"},
			protectSettings{UserPW: "s3cret", OwnerPW: "s3cret", Permissions: model.PermissionsPrint, EncryptUsingAES: true, EncryptKeyLength: 256},
			"",
		},
		{
			"split form keeps passwords distinct",
			map[string]interface{}{"userPassword": "open1234", "ownerPassword": "admin1234"},
			protectSettings{UserPW: "open1234", OwnerPW: "admin1234", Permissions: model.PermissionsPrint, EncryptUsingAES: true, EncryptKeyLength: 256},
			"",
		},
		{
			"owner-only opens freely",
			map[string]interface{}{"ownerPassword": "admin1234"},
			protectSettings{UserPW: "", OwnerPW: "admin1234", Permissions: model.PermissionsPrint, EncryptUsingAES: true, EncryptKeyLength: 256},
			"",
		},
		{
			"user-only promotes the owner password",
			map[string]interface{}{"userPassword": "open1234"},
			protectSettings{UserPW: "open1234", OwnerPW: "open1234", Permissions: model.PermissionsPrint, EncryptUsingAES: true, EncryptKeyLength: 256},
			"",
		},
		{
			"mixing legacy and split rejected",
			map[string]interface{}{"password": "s3cret", "ownerPassword": "admin1234"},
			protectSettings{},
			"either password",
		},
		{
			"no password anywhere rejected",
			map[string]interface{}{"permissions": map[string]interface{}{"allowPrint": true}},
			protectSettings{},
			"requires a password",
		},
		{
			"too short",
			map[string]interface{}{"password": "abc"},
			protectSettings{},
			"between 4 and 128",
		},
		{
			"too long",
			map[string]interface{}{"password": strings.Repeat("x", 129)},
			protectSettings{},
			"between 4 and 128",
		},
		{
			"short ownerPassword names the field",
			map[string]interface{}{"userPassword": "open1234", "ownerPassword": "abc"},
			protectSettings{},
			"ownerPassword",
		},
		{
			"aes-128",
			map[string]interface{}{"password": "s3cret", "encryption": "aes-128"},
			protectSettings{UserPW: "s3cret", OwnerPW: "s3cret", Permissions: model.PermissionsPrint, EncryptUsingAES: true, EncryptKeyLength: 128},
			"",
		},
		{
			"rc4-128 case-insensitive",
			map[string]interface{}{"password": "s3cret", "encryption": "RC4-128"},
			protectSettings{UserPW: "s3cret", OwnerPW: "s3cret", Permissions: model.PermissionsPrint, EncryptUsingAES: false, EncryptKeyLength: 128},
			"",
		},
		{
			"unsupported encryption enumerates the choices",
			map[string]interface{}{"password": "s3cret", "encryption": "rc4-40"},
			protectSettings{},
			"aes-256, aes-128, rc4-128",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseProtectOptions(tt.options)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("settings = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestPermissionBits(t *testing.T) {
	if got := permissionBits(nil); got != model.PermissionsPrint {
		t.Errorf("nil = %#x, want PermissionsPrint %#x", got, model.PermissionsPrint)
	}
	allOff := &protectPermissions{AllowPrint: boolPtr(false)}
	if got := permissionBits(allOff); got != model.PermissionsNone {
		t.Errorf("all off = %#x, want PermissionsNone %#x", got, model.PermissionsNone)
	}
	tests := []struct {
		name string
		p    protectPermissions
		want model.PermissionFlags
	}{
		{"copy", protectPermissions{AllowPrint: boolPtr(false), AllowCopy: boolPtr(true)}, model.PermissionsNone | model.PermissionExtract | model.PermissionExtractRev3},
		{"modify", protectPermissions{AllowPrint: boolPtr(false), AllowModify: boolPtr(true)}, model.PermissionsNone | model.PermissionModify},
		{"allowEdit alias", protectPermissions{AllowPrint: boolPtr(false), AllowEdit: boolPtr(true)}, model.PermissionsNone | model.PermissionModify},
		{"allowModify beats the alias", protectPermissions{AllowPrint: boolPtr(false), AllowModify: boolPtr(false), AllowEdit: boolPtr(true)}, model.PermissionsNone},
		{"annotate", protectPermissions{AllowPrint: boolPtr(false), AllowAnnotate: boolPtr(true)}, model.PermissionsNone | model.PermissionModAnnFillForm | model.PermissionFillRev3},
		{"assemble", protectPermissions{AllowPrint: boolPtr(false), AllowAssemble: boolPtr(true)}, model.PermissionsNone | model.PermissionAssembleRev3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := permissionBits(&tt.p); got != tt.want {
				t.Errorf("bits = %#x, want %#x", got, tt.want)
			}
		})
	}
	everything := &protectPermissions{
		AllowPrint:    boolPtr(true),
		AllowCopy:     boolPtr(true),
		AllowModify:   boolPtr(true),
		AllowAnnotate: boolPtr(true),
		AllowAssemble: boolPtr(true),
	}
	if got := permissionBits(everything); got != model.PermissionsAll {
		t.Errorf("everything = %#x, want PermissionsAll %#x", got, model.PermissionsAll)
	}
}

func TestEncryptPDFRoundTrip(t *testing.T) {
	t.Run("split passwords with explicit permissions", func(t *testing.T) {
		input := buildProtectInput(t)
		output := filepath.Join(t.TempDir(), "out.pdf")
		settings := protectSettings{
			UserPW:  "open1234",
			OwnerPW: "admin1234",
			Permissions: model.PermissionsNone |
				model.PermissionPrintRev2 | model.PermissionPrintRev3 |
				model.PermissionExtract | model.PermissionExtractRev3,
			EncryptUsingAES:  true,
			EncryptKeyLength: 256,
		}
		if err := encryptPDF(input, output, settings); err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		ctx, err := readEncrypted(t, output, "open1234", "")
		if err != nil {
			t.Fatalf("reopen with user password: %v", err)
		}
		if ctx.PageCount != 1 {
			t.Errorf("page count = %d, want 1", ctx.PageCount)
		}
		if ctx.E == nil || ctx.E.V != 5 {
			t.Fatalf("encrypt dict V = %+v, want AES-256 (V=5)", ctx.E)
		}
		if int16(ctx.E.P) != int16(settings.Permissions) {
			t.Errorf("P = %#x, want %#x", int16(ctx.E.P), int16(settings.Permissions))
		}
	})

	t.Run("wrong password fails", func(t *testing.T) {
		input := buildProtectInput(t)
		output := filepath.Join(t.TempDir(), "out.pdf")
		settings := protectSettings{UserPW: "open1234", OwnerPW: "admin1234", Permissions: model.PermissionsPrint, EncryptUsingAES: true, EncryptKeyLength: 256}
		if err := encryptPDF(input, output, settings); err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		if _, err := readEncrypted(t, output, "wrongpass", ""); err == nil {
			t.Error("expected wrong-password read to fail")
		}
	})

	t.Run("owner-only output opens with no password", func(t *testing.T) {
		input := buildProtectInput(t)
		output := filepath.Join(t.TempDir(), "out.pdf")
		settings := protectSettings{UserPW: "", OwnerPW: "admin1234", Permissions: model.PermissionsNone, EncryptUsingAES: true, EncryptKeyLength: 256}
		if err := encryptPDF(input, output, settings); err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		ctx, err := readEncrypted(t, output, "", "")
		if err != nil {
			t.Fatalf("password-less open of owner-only file: %v", err)
		}
		want := model.PermissionsNone
		if int16(ctx.E.P) != int16(want) {
			t.Errorf("P = %#x, want restricted %#x", int16(ctx.E.P), int16(want))
		}
	})

	t.Run("aes-128 and rc4-128 pin the cipher", func(t *testing.T) {
		// pdfcpu writes V=4 with crypt filters for BOTH 128-bit modes; the
		// cipher shows in the plaintext /CFM name (AESV2 vs V2) of the
		// Encrypt dict, so the raw output bytes are the assertion target.
		for _, tc := range []struct {
			name    string
			aes     bool
			wantCFM string
		}{
			{"aes-128", true, "AESV2"},
			{"rc4-128", false, "V2"},
		} {
			input := buildProtectInput(t)
			output := filepath.Join(t.TempDir(), tc.name+".pdf")
			settings := protectSettings{UserPW: "open1234", OwnerPW: "open1234", Permissions: model.PermissionsPrint, EncryptUsingAES: tc.aes, EncryptKeyLength: 128}
			if err := encryptPDF(input, output, settings); err != nil {
				t.Fatalf("%s encrypt: %v", tc.name, err)
			}
			if _, err := readEncrypted(t, output, "open1234", ""); err != nil {
				t.Fatalf("%s reopen: %v", tc.name, err)
			}
			raw, err := os.ReadFile(output)
			if err != nil {
				t.Fatalf("%s read raw: %v", tc.name, err)
			}
			if !strings.Contains(string(raw), "/CFM /"+tc.wantCFM+" ") &&
				!strings.Contains(string(raw), "/CFM/"+tc.wantCFM) {
				t.Errorf("%s: output does not carry /CFM %s", tc.name, tc.wantCFM)
			}
			if tc.name == "rc4-128" && strings.Contains(string(raw), "AESV") {
				t.Errorf("rc4-128 output unexpectedly uses an AES crypt filter")
			}
		}
	})
}

// TestDecryptPDFOwnerPassword is the regression for the latent unlock bug:
// with only conf.UserPW set, a file whose owner password differs from its
// user password could never be unlocked by its owner.
func TestDecryptPDFOwnerPassword(t *testing.T) {
	input := buildProtectInput(t)
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked.pdf")
	settings := protectSettings{UserPW: "open1234", OwnerPW: "admin1234", Permissions: model.PermissionsPrint, EncryptUsingAES: true, EncryptKeyLength: 256}
	if err := encryptPDF(input, locked, settings); err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	if err := decryptPDF(locked, filepath.Join(dir, "by-owner.pdf"), "admin1234"); err != nil {
		t.Errorf("owner password should unlock: %v", err)
	}
	if err := decryptPDF(locked, filepath.Join(dir, "by-user.pdf"), "open1234"); err != nil {
		t.Errorf("user password should unlock: %v", err)
	}
	if err := decryptPDF(locked, filepath.Join(dir, "by-wrong.pdf"), "wrongpass"); err == nil {
		t.Error("wrong password must not unlock")
	}
	if ctx, err := api.ReadContextFile(filepath.Join(dir, "by-owner.pdf")); err != nil || ctx.PageCount != 1 {
		t.Errorf("decrypted output should open with no password: %v", err)
	}
}

// TestProcessFileProtectDispatch pins the wire shape at the dispatch level:
// the legacy {password} payload still works, and option errors surface as
// parse errors rather than pdfcpu failures.
func TestProcessFileProtectDispatch(t *testing.T) {
	input := buildProtectInput(t)
	res, err := ProcessFile(context.Background(), uuid.New(), "protect-pdf", []string{input},
		map[string]interface{}{"password": "s3cret"}, t.TempDir())
	if err != nil {
		t.Fatalf("legacy payload: %v", err)
	}
	if _, err := readEncrypted(t, res.OutputPath, "s3cret", ""); err != nil {
		t.Errorf("output should open with the password: %v", err)
	}

	if _, err := ProcessFile(context.Background(), uuid.New(), "protect-pdf", []string{input},
		map[string]interface{}{}, t.TempDir()); err == nil ||
		!strings.Contains(err.Error(), "requires a password") {
		t.Errorf("empty options should fail with the parse error, got: %v", err)
	}
}

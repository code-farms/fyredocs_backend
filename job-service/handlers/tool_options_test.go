package handlers

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestValidateToolOptionsPassthrough(t *testing.T) {
	// Empty options always pass.
	if err := validateToolOptions("scan-to-pdf", ""); err != nil {
		t.Errorf("empty options: %v", err)
	}
	if err := validateToolOptions("scan-to-pdf", "   "); err != nil {
		t.Errorf("blank options: %v", err)
	}
	// Non-scan tools have no schema — anything goes (legacy behavior).
	if err := validateToolOptions("compress-pdf", `{"quality":"whatever"}`); err != nil {
		t.Errorf("non-scan tool should pass through: %v", err)
	}
	if err := validateToolOptions("merge-pdf", `not even json`); err != nil {
		t.Errorf("non-scan tool should pass through: %v", err)
	}
}

func TestValidateScanOptions(t *testing.T) {
	corner := func(x, y float64) string {
		return fmt.Sprintf(`{"x":%g,"y":%g}`, x, y)
	}
	fullCorners := fmt.Sprintf(`{"tl":%s,"tr":%s,"br":%s,"bl":%s}`,
		corner(0.1, 0.1), corner(0.9, 0.1), corner(0.9, 0.9), corner(0.1, 0.9))

	tests := []struct {
		name    string
		raw     string
		wantErr string // substring; empty = valid
	}{
		{"valid full payload", fmt.Sprintf(
			`{"ocr":true,"language":"hin","pageSize":"a4","enhance":"bw","pages":[{"corners":%s,"rotation":90}]}`,
			fullCorners), ""},
		{"valid minimal", `{"ocr":false}`, ""},
		{"unknown fields ignored", `{"ocr":true,"futureField":123}`, ""},
		{"case-insensitive enums", `{"pageSize":"A4","enhance":"GRAYSCALE","language":"ENG"}`, ""},
		{"bad json", `{"ocr":`, "valid JSON"},
		{"bad language", `{"language":"jpn"}`, "language"},
		{"scan multi-language", `{"ocr":true,"language":"eng+hin"}`, ""},
		{"scan multi-language unknown part", `{"ocr":true,"language":"eng+jpn"}`, "language"},
		{"scan other pair rejected", `{"ocr":true,"language":"deu+fra"}`, "language"},
		{"bad pageSize", `{"pageSize":"a3"}`, "pageSize"},
		{"business card pageSize", `{"pageSize":"card"}`, ""},
		{"cleanup flags", `{"pages":[{"cleanup":{"shadows":true,"fingers":true,"holes":false,"glare":true,"background":true,"auto":false}}]}`, ""},
		{"cleanup typo rejected", `{"pages":[{"cleanup":{"finger":true}}]}`, "cleanup accepts only"},
		{"cleanup non-bool rejected", `{"pages":[{"cleanup":{"shadows":"yes"}}]}`, "cleanup"},
		{"edgeMids with corners", fmt.Sprintf(`{"pages":[{"corners":%s,"edgeMids":{"top":%s,"right":%s,"bottom":%s,"left":%s}}]}`,
			fullCorners, corner(0.5, 0.15), corner(0.9, 0.5), corner(0.5, 0.9), corner(0.1, 0.5)), ""},
		{"edgeMids without corners", fmt.Sprintf(`{"pages":[{"edgeMids":{"top":%s,"right":%s,"bottom":%s,"left":%s}}]}`,
			corner(0.5, 0.15), corner(0.9, 0.5), corner(0.5, 0.9), corner(0.1, 0.5)), "requires corners"},
		{"edgeMids partial", fmt.Sprintf(`{"pages":[{"corners":%s,"edgeMids":{"top":%s}}]}`, fullCorners, corner(0.5, 0.15)), "edgeMids must include"},
		{"edgeMids out of range", fmt.Sprintf(`{"pages":[{"corners":%s,"edgeMids":{"top":%s,"right":%s,"bottom":%s,"left":%s}}]}`,
			fullCorners, corner(0.5, -0.2), corner(0.9, 0.5), corner(0.5, 0.9), corner(0.1, 0.5)), "normalized"},
		{"bad enhance", `{"enhance":"vivid"}`, "enhance"},
		{"valid margin", `{"pageSize":"a4","margin":"small"}`, ""},
		{"margin none ok", `{"margin":"none"}`, ""},
		{"case-insensitive margin", `{"margin":"NORMAL"}`, ""},
		{"bad margin", `{"margin":"huge"}`, "margin"},
		{"bad rotation", `{"pages":[{"rotation":45}]}`, "rotation"},
		{"rotation 270 ok", `{"pages":[{"rotation":270}]}`, ""},
		{"partial corners", fmt.Sprintf(`{"pages":[{"corners":{"tl":%s}}]}`, corner(0.1, 0.1)), "all four corners"},
		{"corner out of range", fmt.Sprintf(
			`{"pages":[{"corners":{"tl":%s,"tr":%s,"br":%s,"bl":%s}}]}`,
			corner(-0.1, 0.1), corner(0.9, 0.1), corner(0.9, 0.9), corner(0.1, 0.9)), "normalized"},
		{"corner above one", fmt.Sprintf(
			`{"pages":[{"corners":{"tl":%s,"tr":%s,"br":%s,"bl":%s}}]}`,
			corner(0.1, 0.1), corner(1.5, 0.1), corner(0.9, 0.9), corner(0.1, 0.9)), "normalized"},
		{"corner missing y", fmt.Sprintf(
			`{"pages":[{"corners":{"tl":{"x":0.1},"tr":%s,"br":%s,"bl":%s}}]}`,
			corner(0.9, 0.1), corner(0.9, 0.9), corner(0.1, 0.9)), "numeric x and y"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateToolOptions("scan-to-pdf", tt.raw)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateScanOptionsTooManyPages(t *testing.T) {
	pages := make([]string, maxScanPages+1)
	for i := range pages {
		pages[i] = `{}`
	}
	raw := fmt.Sprintf(`{"pages":[%s]}`, strings.Join(pages, ","))
	err := validateToolOptions("scan-to-pdf", raw)
	if err == nil || !strings.Contains(err.Error(), "too many") {
		t.Errorf("error = %v, want too-many-pages error", err)
	}
}

// TestValidateMetadataOptions verifies the pdf-metadata trust boundary: the
// values are written verbatim into the output PDF's Info dictionary, so the
// gate must bound size, reject NUL bytes, and refuse a job with nothing to do.
func TestValidateMetadataOptions(t *testing.T) {
	long := strings.Repeat("x", maxMetadataFieldLen+1)

	tests := []struct {
		name    string
		raw     string
		wantErr string // substring; empty = valid
	}{
		{"single field", `{"title":"Annual Report"}`, ""},
		{"all fields", `{"title":"T","author":"A","subject":"S","keywords":"k1, k2"}`, ""},
		{"whitespace-only fields rejected", `{"title":"   ","author":""}`, "at least one"},
		{"empty object", `{}`, "at least one"},
		{"bad json", `{"title":`, "valid JSON"},
		{"oversized field", fmt.Sprintf(`{"author":%q}`, long), "maximum length"},
		{"nul byte", `{"title":"a\u0000b"}`, "invalid characters"},
		{"unknown fields ignored", `{"title":"ok","futureField":1}`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateToolOptions("pdf-metadata", tt.raw)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestValidateMetadataOptionsEmptyRaw pins the one exception to the
// empty-options passthrough: pdf-metadata's entire purpose is its options, so
// absent options must fail here rather than inside the worker after the
// upload is already consumed.
func TestValidateMetadataOptionsEmptyRaw(t *testing.T) {
	if err := validateToolOptions("pdf-metadata", ""); err == nil {
		t.Error("empty options for pdf-metadata must be rejected")
	}
	if err := validateToolOptions("pdf-metadata", "   "); err == nil {
		t.Error("blank options for pdf-metadata must be rejected")
	}
}

// TestValidateOcrOptions verifies the ocr-pdf trust boundary: language feeds
// a tesseract command line (allowlist — English only until the optimize-pdf
// image ships more packs), and dpi bounds worker memory. Both 300 and "300"
// must parse, matching the worker's optionString coercion.
func TestValidateOcrOptions(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{"empty object", `{}`, ""},
		{"eng", `{"language":"eng"}`, ""},
		{"iso en", `{"language":"en"}`, ""},
		{"case and space folded", `{"language":" ENG "}`, ""},
		{"removed pack spa rejected", `{"language":"spa"}`, "unsupported OCR language"},
		{"removed pack deu rejected", `{"language":"deu"}`, "unsupported OCR language"},
		{"installed pack hin", `{"language":"hin"}`, ""},
		{"english + hindi", `{"language":"eng+hin"}`, ""},
		{"hindi + english", `{"language":"HIN + ENG"}`, ""},
		{"other pair rejected", `{"language":"eng+deu"}`, "unsupported OCR language"},
		{"three languages rejected", `{"language":"eng+hin+deu"}`, "unsupported OCR language"},
		{"unknown part rejected", `{"language":"eng+jpn"}`, "unsupported OCR language"},
		{"duplicate rejected", `{"language":"eng+eng"}`, "unsupported OCR language"},
		{"empty part rejected", `{"language":"eng+"}`, "unsupported OCR language"},
		{"uninstalled pack rejected", `{"language":"jpn"}`, "unsupported OCR language"},
		{"crafted language rejected", `{"language":"eng -c foo"}`, "unsupported OCR language"},
		{"dpi as string", `{"dpi":"300"}`, ""},
		{"dpi as number", `{"dpi":300}`, ""},
		{"dpi empty string is absent", `{"dpi":""}`, ""},
		{"dpi below floor", `{"dpi":71}`, "between 72 and 600"},
		{"dpi above ceiling", `{"dpi":"601"}`, "between 72 and 600"},
		{"dpi not a number", `{"dpi":"lots"}`, "whole number"},
		{"not json", `nope`, "valid JSON"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateToolOptions("ocr-pdf", tt.raw)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestValidateOcrOptionsEmptyRaw pins that absent options stay valid for
// ocr-pdf (defaults: eng, worker-side DPI default).
func TestValidateOcrOptionsEmptyRaw(t *testing.T) {
	if err := validateToolOptions("ocr-pdf", ""); err != nil {
		t.Errorf("empty options must pass for ocr-pdf: %v", err)
	}
}

// TestValidatePdfToImageOptions verifies the pdf-to-image trust boundary:
// format selects a pdftoppm flag on the worker command line (allowlist), and
// dpi bounds worker memory (uncompressed TIFF is the worst case).
func TestValidatePdfToImageOptions(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{"empty object", `{}`, ""},
		{"png", `{"format":"png"}`, ""},
		{"jpg", `{"format":"jpg"}`, ""},
		{"jpeg alias", `{"format":"JPEG"}`, ""},
		{"tiff", `{"format":"tiff","dpi":"300"}`, ""},
		{"unknown format", `{"format":"bmp"}`, "unsupported image format"},
		{"crafted format", `{"format":"png -r 99999"}`, "unsupported image format"},
		{"dpi as number", `{"dpi":150}`, ""},
		{"dpi below floor", `{"dpi":"71"}`, "between 72 and 600"},
		{"dpi above ceiling", `{"dpi":601}`, "between 72 and 600"},
		{"dpi not numeric", `{"dpi":"max"}`, "whole number"},
		{"not json", `[]`, "valid JSON"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateToolOptions("pdf-to-image", tt.raw)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestValidateSignOptions verifies the sign-pdf trust boundary: signatureData
// is attacker-controlled base64 written to disk and decoded by pdfcpu, so the
// gate bounds size, requires a png/jpeg data-URL, and allowlists the anchor.
func TestValidateSignOptions(t *testing.T) {
	tinyPng := `{"signatureData":"data:image/png;base64,iVBORw0KGgo="}`
	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{"minimal png", tinyPng, ""},
		{"jpeg allowed", `{"signatureData":"data:image/jpeg;base64,aGVsbG8="}`, ""},
		{"anchor + page string", `{"signatureData":"data:image/png;base64,aGVsbG8=","position":"tl","page":"3"}`, ""},
		{"page number zero (last page)", `{"signatureData":"data:image/png;base64,aGVsbG8=","page":0}`, ""},
		{"missing signature", `{"position":"br"}`, "requires options with signatureData"},
		{"webp rejected", `{"signatureData":"data:image/webp;base64,aGVsbG8="}`, "image/png or image/jpeg"},
		{"raw base64 without prefix", `{"signatureData":"aGVsbG8="}`, "image/png or image/jpeg"},
		{"bad base64", `{"signatureData":"data:image/png;base64,%%%%"}`, "not valid base64"},
		{"unknown anchor", `{"signatureData":"data:image/png;base64,aGVsbG8=","position":"top-left"}`, "unsupported position"},
		{"negative page", `{"signatureData":"data:image/png;base64,aGVsbG8=","page":-2}`, "0 (last page) or a positive"},
		{"page garbage", `{"signatureData":"data:image/png;base64,aGVsbG8=","page":"three"}`, "whole number"},
		{"not json", `nope`, "valid JSON"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateToolOptions("sign-pdf", tt.raw)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestValidateSignOptionsSizeCap pins the decoded-size bound: an oversized
// signature must be rejected without decoding it.
func TestValidateSignOptionsSizeCap(t *testing.T) {
	big := strings.Repeat("A", (maxSignatureBytes/3*4)+8)
	raw := `{"signatureData":"data:image/png;base64,` + big + `"}`
	if err := validateToolOptions("sign-pdf", raw); err == nil || !strings.Contains(err.Error(), "maximum size") {
		t.Errorf("oversized signature not rejected: %v", err)
	}
}

// TestValidateSignOptionsEmptyRaw pins that absent options fail: the tool is
// useless without a signature (pdf-metadata precedent).
func TestValidateSignOptionsEmptyRaw(t *testing.T) {
	if err := validateToolOptions("sign-pdf", ""); err == nil {
		t.Error("empty options for sign-pdf must be rejected")
	}
}

func TestValidateProtectOptions(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{"legacy password", `{"password":"s3cret"}`, ""},
		{"split form", `{"userPassword":"open1234","ownerPassword":"admin1234"}`, ""},
		{"owner-only", `{"ownerPassword":"admin1234"}`, ""},
		{"user-only", `{"userPassword":"open1234"}`, ""},
		{"permissions and encryption ride along", `{"password":"s3cret","permissions":{"allowPrint":false,"allowCopy":true},"encryption":"aes-128"}`, ""},
		{"allowEdit legacy alias accepted", `{"password":"s3cret","permissions":{"allowEdit":true}}`, ""},
		{"unknown permission keys tolerated", `{"password":"s3cret","permissions":{"futureFlag":true}}`, ""},
		{"confirmPassword tolerated", `{"password":"s3cret","confirmPassword":"s3cret"}`, ""},
		{"encryption case-insensitive", `{"password":"s3cret","encryption":"AES-256"}`, ""},
		{"rc4-128 accepted", `{"password":"s3cret","encryption":"rc4-128"}`, ""},
		{"mixing password with userPassword", `{"password":"s3cret","userPassword":"open1234"}`, "either password"},
		{"mixing password with ownerPassword", `{"password":"s3cret","ownerPassword":"admin1234"}`, "either password"},
		{"no password anywhere", `{"permissions":{"allowPrint":true}}`, "requires a password"},
		{"password too short", `{"password":"abc"}`, "between 4 and 128"},
		{"password too long", `{"password":"` + strings.Repeat("x", 129) + `"}`, "between 4 and 128"},
		{"short userPassword names the field", `{"userPassword":"abc","ownerPassword":"admin1234"}`, "userPassword"},
		{"short ownerPassword names the field", `{"userPassword":"open1234","ownerPassword":"abc"}`, "ownerPassword"},
		{"unsupported encryption", `{"password":"s3cret","encryption":"rc4-40"}`, "aes-256, aes-128, rc4-128"},
		{"wrong-typed permissions", `{"password":"s3cret","permissions":{"allowPrint":"yes"}}`, "valid JSON"},
		{"numeric password rejected", `{"password":1234}`, "valid JSON"},
		{"bad json", `{"password":`, "valid JSON"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateToolOptions("protect-pdf", tt.raw)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateProtectOptionsEmptyRaw(t *testing.T) {
	for _, raw := range []string{"", "   "} {
		if err := validateToolOptions("protect-pdf", raw); err == nil ||
			!strings.Contains(err.Error(), "requires options") {
			t.Errorf("empty raw %q should be rejected, got: %v", raw, err)
		}
	}
}

func TestValidateUnlockOptions(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{"any non-empty password", `{"password":"x"}`, ""},
		{"missing password", `{}`, "current password"},
		{"empty password", `{"password":""}`, "current password"},
		{"too long", `{"password":"` + strings.Repeat("x", 129) + `"}`, "at most 128"},
		{"bad json", `{"password":`, "valid JSON"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateToolOptions("unlock-pdf", tt.raw)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateUnlockOptionsEmptyRaw(t *testing.T) {
	if err := validateToolOptions("unlock-pdf", ""); err == nil ||
		!strings.Contains(err.Error(), "requires options") {
		t.Errorf("empty raw should be rejected, got: %v", err)
	}
}

// installedPacks returns the tesseract-ocr-data-<lang> packages a Dockerfile
// installs — read from the real file, so an allowlist can never claim a pack
// the image does not ship (the drift that made the ocr tool English-only
// while organize-pdf had five).
func installedPacks(t *testing.T, path string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	packs := map[string]bool{}
	for _, m := range regexp.MustCompile(`tesseract-ocr-data-([a-z_]+)`).FindAllStringSubmatch(string(raw), -1) {
		packs[m[1]] = true
	}
	if len(packs) == 0 {
		t.Fatalf("no tesseract packs found in %s — the pattern or the Dockerfile changed", path)
	}
	return packs
}

// equalCodes compares an allowlist (minus aliases) with installed packs.
func equalCodes(t *testing.T, name string, allow map[string]bool, aliases []string, packs map[string]bool) {
	t.Helper()
	codes := map[string]bool{}
	for k := range allow {
		codes[k] = true
	}
	for _, a := range aliases {
		delete(codes, a)
	}
	for c := range codes {
		if !packs[c] {
			t.Errorf("%s allows %q but the image does not install tesseract-ocr-data-%s", name, c, c)
		}
	}
	for p := range packs {
		if !codes[p] {
			t.Errorf("the image installs %q but %s does not allow it", p, name)
		}
	}
}

// TestOCRLanguagesMatchInstalledPacks pins each allowlist to its worker's
// Dockerfile: ocr-pdf runs in optimize-pdf, scan-to-pdf in organize-pdf.
func TestOCRLanguagesMatchInstalledPacks(t *testing.T) {
	equalCodes(t, "ocrLanguages", ocrLanguages, []string{"", "en"}, installedPacks(t, "../../optimize-pdf/Dockerfile"))
	equalCodes(t, "scanLanguages", scanLanguages, nil, installedPacks(t, "../../organize-pdf/Dockerfile"))
}

func TestValidatePdfToPptOptions(t *testing.T) {
	ok := []string{`{}`, `{"mode":""}`, `{"mode":"editable"}`, `{"mode":"image"}`, `{"mode":" Image "}`}
	for _, raw := range ok {
		if err := validateToolOptions("pdf-to-powerpoint", raw); err != nil {
			t.Errorf("%s should pass: %v", raw, err)
		}
	}
	bad := []string{`{"mode":"svg"}`, `{"mode":"--infilter=x"}`, `not json`}
	for _, raw := range bad {
		if err := validateToolOptions("pdf-to-powerpoint", raw); err == nil {
			t.Errorf("%s should be rejected", raw)
		}
	}
	// Empty options mean the default (editable).
	if err := validateToolOptions("pdf-to-powerpoint", ""); err != nil {
		t.Errorf("empty options: %v", err)
	}
}

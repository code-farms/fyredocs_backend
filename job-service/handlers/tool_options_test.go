package handlers

import (
	"fmt"
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
			`{"ocr":true,"language":"deu","pageSize":"a4","enhance":"bw","pages":[{"corners":%s,"rotation":90}]}`,
			fullCorners), ""},
		{"valid minimal", `{"ocr":false}`, ""},
		{"unknown fields ignored", `{"ocr":true,"futureField":123}`, ""},
		{"case-insensitive enums", `{"pageSize":"A4","enhance":"GRAYSCALE","language":"ENG"}`, ""},
		{"bad json", `{"ocr":`, "valid JSON"},
		{"bad language", `{"language":"jpn"}`, "language"},
		{"bad pageSize", `{"pageSize":"a3"}`, "pageSize"},
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
		{"uninstalled pack rejected", `{"language":"spa"}`, "unsupported OCR language"},
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

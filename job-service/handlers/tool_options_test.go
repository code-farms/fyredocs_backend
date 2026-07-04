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

// Tests for the edit/sign path. The invariants under guard: data-URL images
// land on disk with an extension that matches their mime prefix (a JPEG named
// .png used to ship), and signPDF stamps the requested page without corrupting
// the document — pure pdfcpu, so the round-trip runs everywhere.
package processing

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
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

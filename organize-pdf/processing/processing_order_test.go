package processing

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

func TestParsePageOrderKeepsSequence(t *testing.T) {
	tests := []struct {
		order string
		max   int
		want  []int
	}{
		{"3,1,2", 3, []int{3, 1, 2}},
		{"2-3, 1, 1", 3, []int{2, 3, 1, 1}},
		{"4,x,9,3-1,,2", 5, []int{4, 2}},
		{"5-6,1", 5, []int{1}},
		{"", 3, []int{1, 2, 3}},
		{"all", 2, []int{1, 2}},
		{"9", 3, nil},
	}
	for _, tt := range tests {
		if got := parsePageOrder(tt.order, tt.max); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parsePageOrder(%q, %d) = %v, want %v", tt.order, tt.max, got, tt.want)
		}
	}
}

// TestOrganizePDFReordersPages is the regression test for organize-pdf
// returning pages unchanged: each source page gets a distinct width, so the
// output's page widths reveal the order pdfcpu actually wrote.
func TestOrganizePDFReordersPages(t *testing.T) {
	dir := t.TempDir()
	var pngs []string
	for i, w := range []int{100, 200, 300} {
		p := filepath.Join(dir, string(rune('a'+i))+".png")
		writeTestPNG(t, p, w, 400)
		pngs = append(pngs, p)
	}
	input := filepath.Join(dir, "in.pdf")
	if err := api.ImportImagesFile(pngs, input, nil, nil); err != nil {
		t.Fatalf("build input: %v", err)
	}
	widths := func(path string) []float64 {
		dims, err := api.PageDimsFile(path)
		if err != nil {
			t.Fatalf("page dims %s: %v", path, err)
		}
		out := make([]float64, len(dims))
		for i, d := range dims {
			out[i] = d.Width
		}
		return out
	}
	src := widths(input)
	if src[0] == src[1] || src[1] == src[2] {
		t.Fatalf("fixture pages must differ in width, got %v", src)
	}

	output := filepath.Join(dir, "out.pdf")
	if err := organizePDF(input, output, "3,1,1"); err != nil {
		t.Fatalf("organizePDF: %v", err)
	}
	want := []float64{src[2], src[0], src[0]}
	if got := widths(output); !reflect.DeepEqual(got, want) {
		t.Errorf("page widths = %v, want %v (order 3,1,1)", got, want)
	}
}

func TestOrganizePDFRejectsEmptyOrder(t *testing.T) {
	input := buildProtectInput(t)
	if err := organizePDF(input, filepath.Join(t.TempDir(), "out.pdf"), "7,9"); err == nil {
		t.Error("expected an error when no requested page exists")
	}
}

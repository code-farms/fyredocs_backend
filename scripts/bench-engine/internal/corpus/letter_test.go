package corpus

import (
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

func TestImportOnLetterMakesOneLetterPagePerImage(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "big.png")
	f, err := os.Create(img)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewGray(image.Rect(0, 0, 2550, 3300))); err != nil {
		t.Fatal(err)
	}
	f.Close()
	out := filepath.Join(dir, "out.pdf")
	// Twice: a regeneration must replace the file, not append to it.
	for range 2 {
		if err := importOnLetter(context.Background(), []string{img}, out); err != nil {
			t.Fatal(err)
		}
	}
	dims, err := api.PageDimsFile(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}
	if len(dims) != 1 || int(dims[0].Width) != 612 || int(dims[0].Height) != 792 {
		t.Fatalf("page dims %v, want one 612 x 792 page", dims)
	}
}

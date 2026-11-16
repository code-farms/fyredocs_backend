package corpus

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// drawImage renders a deterministic test image: "gradient" (smooth, compresses
// well), "noise" (incompressible, the compress worst case), "shapes" (flat
// regions and hard edges, like screenshots) or "photo" (gradient + noise +
// shapes, closest to a camera picture).
func drawImage(r *rand.Rand, pattern string, w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	cx, cy := float64(w)*r.Float64(), float64(h)*r.Float64()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var c color.RGBA
			switch pattern {
			case "noise":
				c = color.RGBA{uint8(r.UintN(256)), uint8(r.UintN(256)), uint8(r.UintN(256)), 255}
			case "shapes":
				c = color.RGBA{240, 240, 240, 255}
				if (x/64+y/64)%3 == 0 {
					c = color.RGBA{40, 90, 160, 255}
				}
			default:
				d := math.Hypot(float64(x)-cx, float64(y)-cy) / math.Hypot(float64(w), float64(h))
				c = color.RGBA{uint8(255 * float64(x) / float64(w)), uint8(255 * d), uint8(255 * float64(y) / float64(h)), 255}
				if pattern == "photo" {
					n := int(r.IntN(31)) - 15
					c.R = clamp(int(c.R) + n)
					c.G = clamp(int(c.G) + n)
					c.B = clamp(int(c.B) + n)
					if (x/97+y/71)%5 == 0 {
						c = color.RGBA{c.B, c.R, c.G, 255}
					}
				}
			}
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// clamp limits v to a byte.
func clamp(v int) uint8 { return uint8(max(0, min(255, v))) }

// encodeImage encodes img as "png" or "jpeg" (quality 85).
func encodeImage(img image.Image, format string) ([]byte, error) {
	var b bytes.Buffer
	var err error
	if format == "png" {
		err = png.Encode(&b, img)
	} else {
		err = jpeg.Encode(&b, img, &jpeg.Options{Quality: 85})
	}
	return b.Bytes(), err
}

// genImage: params format (png|jpeg), pattern, w, h.
func genImage(_ context.Context, g *Gen, it Item) (string, error) {
	format := param(it, "format", "jpeg")
	img := drawImage(rng(it), param(it, "pattern", "photo"), intParam(it, "w", 1600), intParam(it, "h", 1200))
	data, err := encodeImage(img, format)
	if err != nil {
		return "", err
	}
	ext := map[string]string{"png": "png", "jpeg": "jpg"}[format]
	if ext == "" {
		return "", errUnsupported(it, "format "+format)
	}
	abs, rel, err := g.outPath(it, ext)
	if err != nil {
		return "", err
	}
	return rel, os.WriteFile(abs, data, 0o644)
}

// genImagePDF: params images (count, default 4), pattern, format, w, h. Images
// are imported one per page with pdfcpu, the same importer image-to-pdf uses.
func genImagePDF(ctx context.Context, g *Gen, it Item) (string, error) {
	r := rng(it)
	n := intParam(it, "images", 4)
	dir, err := os.MkdirTemp(g.Scratch, "imagepdf-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	var files []string
	for i := 0; i < n; i++ {
		img := drawImage(r, param(it, "pattern", "photo"), intParam(it, "w", 1600), intParam(it, "h", 1200))
		format := param(it, "format", "jpeg")
		data, err := encodeImage(img, format)
		if err != nil {
			return "", err
		}
		p := filepath.Join(dir, "img"+string(rune('a'+i))+"."+map[string]string{"png": "png", "jpeg": "jpg"}[format])
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return "", err
		}
		files = append(files, p)
	}
	abs, rel, err := g.outPath(it, "pdf")
	if err != nil {
		return "", err
	}
	return rel, importOnLetter(ctx, files, abs)
}

// importOnLetter imports one image per US Letter page, centred and scaled to
// fit (position:full would keep the image size as the page size). With
// no import config pdfcpu makes each page the image's pixel size in points, so
// a 300 dpi scan became a 2550 x 3300 pt (35 x 46 in) page and pdf-to-image had
// to rasterise posters (found 2026-11-13: 31 s per image-heavy PDF).
func importOnLetter(ctx context.Context, images []string, out string) error {
	imp, err := api.Import("formsize:Letter, position:c, scalefactor:1.0 rel", types.POINTS)
	if err != nil {
		return err
	}
	// ImportImagesFile APPENDS to an existing out, so a -force regeneration
	// kept the old pages in front of the new ones.
	if err := os.Remove(out); err != nil && !os.IsNotExist(err) {
		return err
	}
	return api.ImportImagesFile(ctx, images, out, imp, nil)
}

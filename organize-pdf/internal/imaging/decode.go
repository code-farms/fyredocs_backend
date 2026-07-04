package imaging

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"

	// Register the webp decoder alongside the jpeg/png ones imported above.
	_ "golang.org/x/image/webp"
)

// MaxDecodePixels caps decoded image size (width × height) to bound memory.
const MaxDecodePixels = 50_000_000 // 50 MP

// DecodeReader decodes an image from r after a DecodeConfig size guard.
// The reader must support being fully buffered; callers stream from disk or
// object storage.
func DecodeReader(r io.Reader) (image.Image, string, error) {
	// Buffer the bytes so we can DecodeConfig then Decode.
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, "", fmt.Errorf("imaging: read: %w", err)
	}
	return decodeBytes(data)
}

// DecodeFile decodes an image file with the same guards as DecodeReader.
func DecodeFile(path string) (image.Image, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("imaging: read %s: %w", path, err)
	}
	return decodeBytes(data)
}

func decodeBytes(data []byte) (image.Image, string, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("imaging: unsupported or corrupt image: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > MaxDecodePixels {
		return nil, "", fmt.Errorf("imaging: image %dx%d exceeds %d pixel limit", cfg.Width, cfg.Height, MaxDecodePixels)
	}
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("imaging: decode: %w", err)
	}
	return img, format, nil
}

// EncodeJPEG writes img to path as JPEG at the given quality.
func EncodeJPEG(path string, img image.Image, quality int) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("imaging: create %s: %w", path, err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: quality}); err != nil {
		return fmt.Errorf("imaging: encode jpeg %s: %w", path, err)
	}
	return nil
}

// EncodePNG writes img to path as PNG — used for bilevel (bw) output where
// JPEG artifacts would speckle the thresholded image.
func EncodePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("imaging: create %s: %w", path, err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return fmt.Errorf("imaging: encode png %s: %w", path, err)
	}
	return nil
}

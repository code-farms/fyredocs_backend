package imaging

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func testImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 100, 255})
		}
	}
	return img
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	dir := t.TempDir()

	jpgPath := filepath.Join(dir, "t.jpg")
	if err := EncodeJPEG(jpgPath, testImage(40, 30), 92); err != nil {
		t.Fatalf("EncodeJPEG: %v", err)
	}
	img, format, err := DecodeFile(jpgPath)
	if err != nil {
		t.Fatalf("DecodeFile jpg: %v", err)
	}
	if format != "jpeg" || img.Bounds().Dx() != 40 || img.Bounds().Dy() != 30 {
		t.Errorf("jpg round trip: format=%s bounds=%v", format, img.Bounds())
	}

	pngPath := filepath.Join(dir, "t.png")
	if err := EncodePNG(pngPath, testImage(20, 25)); err != nil {
		t.Fatalf("EncodePNG: %v", err)
	}
	img, format, err = DecodeFile(pngPath)
	if err != nil {
		t.Fatalf("DecodeFile png: %v", err)
	}
	if format != "png" || img.Bounds().Dx() != 20 {
		t.Errorf("png round trip: format=%s bounds=%v", format, img.Bounds())
	}
}

func TestDecodeReaderMatchesFile(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, testImage(10, 10)); err != nil {
		t.Fatal(err)
	}
	img, format, err := DecodeReader(&buf)
	if err != nil {
		t.Fatalf("DecodeReader: %v", err)
	}
	if format != "png" || img.Bounds().Dx() != 10 {
		t.Errorf("got format=%s bounds=%v", format, img.Bounds())
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.jpg")
	if err := os.WriteFile(bad, []byte("not an image at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := DecodeFile(bad); err == nil {
		t.Error("expected error decoding garbage")
	}
}

func TestDecodeRejectsOversize(t *testing.T) {
	// Craft a PNG header claiming enormous dimensions without allocating one:
	// encode a tiny image, then patch the IHDR width/height fields.
	var buf bytes.Buffer
	if err := png.Encode(&buf, testImage(1, 1)); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	// IHDR: bytes 16-19 width, 20-23 height (big endian).
	patch := func(off int, v uint32) {
		data[off] = byte(v >> 24)
		data[off+1] = byte(v >> 16)
		data[off+2] = byte(v >> 8)
		data[off+3] = byte(v)
	}
	patch(16, 100000)
	patch(20, 100000) // 10 000 MP ≫ 50 MP cap

	_, _, err := DecodeReader(bytes.NewReader(data))
	if err == nil {
		t.Error("expected oversize rejection")
	}
}

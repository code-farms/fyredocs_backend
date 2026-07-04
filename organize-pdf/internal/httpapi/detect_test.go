package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"fyredocs/shared/storage"
)

type fakeGetter struct {
	objects map[string][]byte
	sizes   map[string]int64 // overrides for stat size
}

func (f *fakeGetter) GetObject(_ context.Context, bucket, key string) (io.ReadCloser, error) {
	data, ok := f.objects[bucket+"/"+key]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (f *fakeGetter) StatObject(_ context.Context, bucket, key string) (storage.ObjectInfo, error) {
	data, ok := f.objects[bucket+"/"+key]
	if !ok {
		return storage.ObjectInfo{}, fmt.Errorf("not found")
	}
	size := int64(len(data))
	if s, ok := f.sizes[bucket+"/"+key]; ok {
		size = s
	}
	return storage.ObjectInfo{Key: key, Size: size}, nil
}

func (f *fakeGetter) BucketUploads() string { return "uploads" }

func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{220, 220, 210, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newRouter(store ObjectGetter, token string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterInternalRoutes(r, store, token)
	return r
}

func doDetect(r *gin.Engine, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/detect-edges", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestDetectEdgesHappyPath(t *testing.T) {
	store := &fakeGetter{objects: map[string][]byte{
		"uploads/photo.png": encodePNG(t, 200, 260),
	}}
	w := doDetect(newRouter(store, ""), `{"key":"photo.png"}`, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var env struct {
		Success bool `json:"success"`
		Data    struct {
			Corners    map[string]map[string]float64 `json:"corners"`
			Confidence float64                       `json:"confidence"`
			Width      int                           `json:"width"`
			Height     int                           `json:"height"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("bad envelope: %v", err)
	}
	if !env.Success || env.Data.Width != 200 || env.Data.Height != 260 {
		t.Errorf("unexpected payload: %+v", env.Data)
	}
	for _, corner := range []string{"tl", "tr", "br", "bl"} {
		if _, ok := env.Data.Corners[corner]; !ok {
			t.Errorf("missing corner %s", corner)
		}
	}
	// A featureless image must fall back to confidence 0 (full-image quad).
	if env.Data.Confidence != 0 {
		t.Errorf("blank image confidence = %f, want 0", env.Data.Confidence)
	}
}

func TestDetectEdgesMissingObject(t *testing.T) {
	w := doDetect(newRouter(&fakeGetter{objects: map[string][]byte{}}, ""), `{"key":"nope.png"}`, nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", w.Code)
	}
}

func TestDetectEdgesMissingKey(t *testing.T) {
	w := doDetect(newRouter(&fakeGetter{objects: map[string][]byte{}}, ""), `{}`, nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", w.Code)
	}
}

func TestDetectEdgesOversize(t *testing.T) {
	store := &fakeGetter{
		objects: map[string][]byte{"uploads/big.png": encodePNG(t, 10, 10)},
		sizes:   map[string]int64{"uploads/big.png": maxDetectObjectBytes + 1},
	}
	w := doDetect(newRouter(store, ""), `{"key":"big.png"}`, nil)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status %d, want 413", w.Code)
	}
}

func TestDetectEdgesUndecodable(t *testing.T) {
	store := &fakeGetter{objects: map[string][]byte{
		"uploads/junk.bin": []byte("this is not an image"),
	}}
	w := doDetect(newRouter(store, ""), `{"key":"junk.bin"}`, nil)
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status %d, want 422", w.Code)
	}
}

func TestDetectEdgesTokenAuth(t *testing.T) {
	store := &fakeGetter{objects: map[string][]byte{
		"uploads/photo.png": encodePNG(t, 64, 64),
	}}
	r := newRouter(store, "secret")

	if w := doDetect(r, `{"key":"photo.png"}`, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("missing token: status %d, want 401", w.Code)
	}
	if w := doDetect(r, `{"key":"photo.png"}`, map[string]string{"X-Internal-Token": "wrong"}); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: status %d, want 401", w.Code)
	}
	if w := doDetect(r, `{"key":"photo.png"}`, map[string]string{"X-Internal-Token": "secret"}); w.Code != http.StatusOK {
		t.Errorf("valid token: status %d, want 200", w.Code)
	}
}

func TestDetectEdgesCustomBucket(t *testing.T) {
	store := &fakeGetter{objects: map[string][]byte{
		"other/photo.png": encodePNG(t, 64, 64),
	}}
	w := doDetect(newRouter(store, ""), `{"bucket":"other","key":"photo.png"}`, nil)
	if w.Code != http.StatusOK {
		t.Errorf("status %d, want 200", w.Code)
	}
}

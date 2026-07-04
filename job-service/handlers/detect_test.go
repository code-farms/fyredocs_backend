package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func detectTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			img.Set(x, y, color.RGBA{200, 200, 200, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func doDetectRequest(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/organize-pdf/detect-edges", DetectEdges)

	req := httptest.NewRequest(http.MethodPost, "/api/organize-pdf/detect-edges", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestDetectEdgesHappyPathRelaysUpstream(t *testing.T) {
	_, client := withMiniRedis(t)
	fs := withFakeStore(t)
	seedUploadObject(t, client, fs, "up-1", "photo.png", detectTestPNG(t))

	var gotKey string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Key string `json:"key"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotKey = req.Key
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"message":"Edges detected","data":{"corners":{"tl":{"x":0.1,"y":0.1},"tr":{"x":0.9,"y":0.1},"br":{"x":0.9,"y":0.9},"bl":{"x":0.1,"y":0.9}},"confidence":0.8,"width":20,"height":20}}`))
	}))
	defer upstream.Close()
	t.Setenv("ORGANIZE_PDF_URL", upstream.URL)

	w := doDetectRequest(t, `{"uploadId":"up-1"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if gotKey != "uploads/up-1/photo.png" {
		t.Errorf("upstream key = %q", gotKey)
	}

	var env struct {
		Success bool `json:"success"`
		Data    struct {
			Confidence float64 `json:"confidence"`
			Width      int     `json:"width"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("bad envelope: %v", err)
	}
	if !env.Success || env.Data.Confidence != 0.8 || env.Data.Width != 20 {
		t.Errorf("unexpected relay payload: %s", w.Body.String())
	}

	// Critically: the upload state must survive (peek, not consume).
	state, err := client.HGetAll(context.Background(), "upload:up-1").Result()
	if err != nil || len(state) == 0 {
		t.Error("upload state must NOT be deleted by detect-edges")
	}
}

func TestDetectEdgesMissingUploadID(t *testing.T) {
	withMiniRedis(t)
	withFakeStore(t)
	if w := doDetectRequest(t, `{}`); w.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", w.Code)
	}
	if w := doDetectRequest(t, `not json`); w.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", w.Code)
	}
}

func TestDetectEdgesUnknownUpload(t *testing.T) {
	withMiniRedis(t)
	withFakeStore(t)
	if w := doDetectRequest(t, `{"uploadId":"ghost"}`); w.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", w.Code)
	}
}

func TestDetectEdgesRejectsPDF(t *testing.T) {
	_, client := withMiniRedis(t)
	fs := withFakeStore(t)
	// A PDF upload must be rejected — either by the image-only MIME sniff in
	// consumeUpload or by detect-edges' own extension guard. Both are 400.
	seedUploadObject(t, client, fs, "up-pdf", "doc.pdf", []byte("%PDF-1.4 fake pdf content"))

	w := doDetectRequest(t, `{"uploadId":"up-pdf"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400: %s", w.Code, w.Body.String())
	}
}

func TestDetectEdgesUpstreamDown(t *testing.T) {
	_, client := withMiniRedis(t)
	fs := withFakeStore(t)
	seedUploadObject(t, client, fs, "up-2", "photo.png", detectTestPNG(t))

	// Point at a closed server.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead.Close()
	t.Setenv("ORGANIZE_PDF_URL", dead.URL)

	w := doDetectRequest(t, `{"uploadId":"up-2"}`)
	if w.Code != http.StatusBadGateway {
		t.Errorf("status %d, want 502: %s", w.Code, w.Body.String())
	}
}

func TestDetectEdgesUpstream5xx(t *testing.T) {
	_, client := withMiniRedis(t)
	fs := withFakeStore(t)
	seedUploadObject(t, client, fs, "up-3", "photo.png", detectTestPNG(t))

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer upstream.Close()
	t.Setenv("ORGANIZE_PDF_URL", upstream.URL)

	w := doDetectRequest(t, `{"uploadId":"up-3"}`)
	if w.Code != http.StatusBadGateway {
		t.Errorf("status %d, want 502", w.Code)
	}
}

func TestDetectEdgesUpstream4xxRelaysAs422(t *testing.T) {
	_, client := withMiniRedis(t)
	fs := withFakeStore(t)
	seedUploadObject(t, client, fs, "up-4", "photo.png", detectTestPNG(t))

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"success":false,"message":"","error":{"code":"UNDECODABLE_IMAGE","details":"The upload is not a decodable image."}}`))
	}))
	defer upstream.Close()
	t.Setenv("ORGANIZE_PDF_URL", upstream.URL)

	w := doDetectRequest(t, `{"uploadId":"up-4"}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status %d, want 422", w.Code)
	}
	if !strings.Contains(w.Body.String(), "UNDECODABLE_IMAGE") {
		t.Errorf("expected relayed code, got %s", w.Body.String())
	}
}

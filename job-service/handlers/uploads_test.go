package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// apiEnvelope mirrors the standard {success,message,data,error} response
// envelope used by every handler.
type apiEnvelope struct {
	Success bool                   `json:"success"`
	Message string                 `json:"message"`
	Data    map[string]interface{} `json:"data"`
	Error   *struct {
		Code    string `json:"code"`
		Details string `json:"details"`
	} `json:"error"`
}

func newUploadTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/uploads/init", InitUpload)
	r.GET("/api/uploads/:uploadId/parts", GetUploadParts)
	r.POST("/api/uploads/:uploadId/complete", CompleteUpload)
	r.GET("/api/uploads/:uploadId/status", GetUploadStatus)
	r.DELETE("/api/uploads/:uploadId", AbortUpload)
	r.PUT("/api/uploads/:uploadId/chunk", UploadChunk)
	return r
}

func doJSON(t *testing.T, r *gin.Engine, method, path string, body interface{}, headers map[string]string) (*httptest.ResponseRecorder, apiEnvelope) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var env apiEnvelope
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode envelope (%d %s): %v", rec.Code, rec.Body.String(), err)
		}
	}
	return rec, env
}

func initUpload(t *testing.T, r *gin.Engine, fileName string, fileSize int64, planMB string) (*httptest.ResponseRecorder, apiEnvelope) {
	t.Helper()
	headers := map[string]string{}
	if planMB != "" {
		headers["X-User-Plan-Max-File-MB"] = planMB
	}
	return doJSON(t, r, http.MethodPost, "/api/uploads/init", UploadInitRequest{
		FileName:    fileName,
		FileSize:    fileSize,
		ContentType: "application/pdf",
	}, headers)
}

func TestInitUpload_HappyPath(t *testing.T) {
	_, client := withMiniRedis(t)
	fs := withFakeStore(t)
	r := newUploadTestRouter()

	const fileSize = 20 * 1024 * 1024 // 20 MiB → 3 parts at 8 MiB
	rec, env := initUpload(t, r, "report.pdf", fileSize, "50")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}

	uploadID, _ := env.Data["uploadId"].(string)
	if uploadID == "" {
		t.Fatal("missing uploadId in response")
	}
	key, _ := env.Data["key"].(string)
	wantKey := fmt.Sprintf("uploads/%s/report.pdf", uploadID)
	if key != wantKey {
		t.Errorf("key = %q, want %q", key, wantKey)
	}
	if got := int64(env.Data["partSize"].(float64)); got != 8*1024*1024 {
		t.Errorf("partSize = %d, want %d", got, 8*1024*1024)
	}
	if got := int(env.Data["totalParts"].(float64)); got != 3 {
		t.Errorf("totalParts = %d, want 3", got)
	}
	parts, _ := env.Data["parts"].([]interface{})
	if len(parts) != 3 {
		t.Fatalf("len(parts) = %d, want 3", len(parts))
	}
	first := parts[0].(map[string]interface{})
	if int(first["partNumber"].(float64)) != 1 {
		t.Errorf("first partNumber = %v, want 1", first["partNumber"])
	}
	if u, _ := first["url"].(string); !strings.Contains(u, "partNumber=1") {
		t.Errorf("first part URL %q should contain partNumber=1", u)
	}
	if env.Data["urlExpiresAt"] == "" {
		t.Error("missing urlExpiresAt")
	}

	// One multipart upload must be open against the uploads bucket/key.
	if len(fs.multiparts) != 1 {
		t.Fatalf("multiparts = %d, want 1", len(fs.multiparts))
	}
	for _, mp := range fs.multiparts {
		if mp.bucket != fs.BucketUploads() || mp.key != wantKey {
			t.Errorf("multipart at %s/%s, want %s/%s", mp.bucket, mp.key, fs.BucketUploads(), wantKey)
		}
	}

	// Redis session state with TTL.
	state, err := client.HGetAll(context.Background(), "upload:"+uploadID).Result()
	if err != nil || len(state) == 0 {
		t.Fatalf("redis state missing: %v", err)
	}
	if state["fileName"] != "report.pdf" || state["key"] != wantKey || state["s3UploadId"] == "" {
		t.Errorf("unexpected redis state: %v", state)
	}
	if ttl := client.TTL(context.Background(), "upload:"+uploadID).Val(); ttl <= 0 {
		t.Errorf("upload state must have a TTL, got %v", ttl)
	}
}

func TestInitUpload_PlanLimitRejectedBeforePresigning(t *testing.T) {
	withMiniRedis(t)
	fs := withFakeStore(t)
	r := newUploadTestRouter()

	rec, env := initUpload(t, r, "big.pdf", 30*1024*1024, "10")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	if env.Error == nil || env.Error.Code != "FILE_TOO_LARGE" {
		t.Errorf("error code = %v, want FILE_TOO_LARGE", env.Error)
	}
	if len(fs.multiparts) != 0 {
		t.Errorf("no multipart upload may be created for an oversized file, got %d", len(fs.multiparts))
	}
}

func TestInitUpload_PartCountCap(t *testing.T) {
	withMiniRedis(t)
	fs := withFakeStore(t)
	r := newUploadTestRouter()

	// 1001 parts at the default 8 MiB part size.
	fileSize := int64(8*1024*1024)*1000 + 1
	rec, env := initUpload(t, r, "huge.pdf", fileSize, "100000")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if env.Error == nil || env.Error.Code != "INVALID_INPUT" {
		t.Errorf("error code = %v, want INVALID_INPUT", env.Error)
	}
	if len(fs.multiparts) != 0 {
		t.Errorf("no multipart upload may be created past the part cap, got %d", len(fs.multiparts))
	}
}

func TestInitUpload_SanitizesFileName(t *testing.T) {
	withMiniRedis(t)
	withFakeStore(t)
	r := newUploadTestRouter()

	rec, env := initUpload(t, r, "../../etc/passwd", 1024, "50")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	key, _ := env.Data["key"].(string)
	uploadID, _ := env.Data["uploadId"].(string)
	if key != fmt.Sprintf("uploads/%s/passwd", uploadID) {
		t.Errorf("key = %q, want sanitized base name under uploads/%s/", key, uploadID)
	}
}

func TestGetUploadParts_RepresignsRequestedParts(t *testing.T) {
	withMiniRedis(t)
	withFakeStore(t)
	r := newUploadTestRouter()

	_, env := initUpload(t, r, "report.pdf", 20*1024*1024, "50")
	uploadID := env.Data["uploadId"].(string)

	rec, env := doJSON(t, r, http.MethodGet, "/api/uploads/"+uploadID+"/parts?partNumbers=2,3", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	parts, _ := env.Data["parts"].([]interface{})
	if len(parts) != 2 {
		t.Fatalf("len(parts) = %d, want 2", len(parts))
	}
	for i, want := range []int{2, 3} {
		p := parts[i].(map[string]interface{})
		if int(p["partNumber"].(float64)) != want {
			t.Errorf("parts[%d].partNumber = %v, want %d", i, p["partNumber"], want)
		}
		if u, _ := p["url"].(string); !strings.Contains(u, fmt.Sprintf("partNumber=%d", want)) {
			t.Errorf("parts[%d].url = %q, want partNumber=%d", i, u, want)
		}
	}
}

func TestGetUploadParts_AllPartsWhenOmitted(t *testing.T) {
	withMiniRedis(t)
	withFakeStore(t)
	r := newUploadTestRouter()

	_, env := initUpload(t, r, "report.pdf", 20*1024*1024, "50")
	uploadID := env.Data["uploadId"].(string)

	rec, env := doJSON(t, r, http.MethodGet, "/api/uploads/"+uploadID+"/parts", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if parts, _ := env.Data["parts"].([]interface{}); len(parts) != 3 {
		t.Errorf("len(parts) = %d, want all 3", len(parts))
	}
}

func TestGetUploadParts_SessionGone(t *testing.T) {
	withMiniRedis(t)
	withFakeStore(t)
	r := newUploadTestRouter()

	rec, env := doJSON(t, r, http.MethodGet, "/api/uploads/nope/parts", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if env.Error == nil || env.Error.Code != "NOT_FOUND" {
		t.Errorf("error code = %v, want NOT_FOUND", env.Error)
	}
}

func completeBody(parts ...int) UploadCompleteRequest {
	req := UploadCompleteRequest{}
	for _, n := range parts {
		req.Parts = append(req.Parts, UploadCompletedPart{PartNumber: n, ETag: fmt.Sprintf("etag-%d", n)})
	}
	return req
}

func TestCompleteUpload_PartCountMismatch(t *testing.T) {
	withMiniRedis(t)
	withFakeStore(t)
	r := newUploadTestRouter()

	_, env := initUpload(t, r, "report.pdf", 20*1024*1024, "50") // 3 parts
	uploadID := env.Data["uploadId"].(string)

	rec, env := doJSON(t, r, http.MethodPost, "/api/uploads/"+uploadID+"/complete", completeBody(1, 2), nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if env.Error == nil || env.Error.Code != "UPLOAD_INCOMPLETE" {
		t.Errorf("error code = %v, want UPLOAD_INCOMPLETE", env.Error)
	}
}

func TestCompleteUpload_OversizeRemovedAfterTrueSizeCheck(t *testing.T) {
	_, client := withMiniRedis(t)
	fs := withFakeStore(t)
	fs.completeSize = 60 * 1024 * 1024 // true size exceeds the 50MB plan
	r := newUploadTestRouter()

	_, env := initUpload(t, r, "report.pdf", 20*1024*1024, "50")
	uploadID := env.Data["uploadId"].(string)
	key := env.Data["key"].(string)

	rec, env := doJSON(t, r, http.MethodPost, "/api/uploads/"+uploadID+"/complete", completeBody(1, 2, 3),
		map[string]string{"X-User-Plan-Max-File-MB": "50"})
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body %s)", rec.Code, rec.Body.String())
	}
	if env.Error == nil || env.Error.Code != "FILE_TOO_LARGE" {
		t.Errorf("error code = %v, want FILE_TOO_LARGE", env.Error)
	}
	wantRemoved := objKey(fs.BucketUploads(), key)
	found := false
	for _, r := range fs.removed {
		if r == wantRemoved {
			found = true
		}
	}
	if !found {
		t.Errorf("oversized object %s must be removed, removed = %v", wantRemoved, fs.removed)
	}
	if n, _ := client.Exists(context.Background(), "upload:"+uploadID).Result(); n != 0 {
		t.Error("redis state must be deleted after oversize rejection")
	}
}

func TestCompleteUpload_HappyPath(t *testing.T) {
	_, client := withMiniRedis(t)
	fs := withFakeStore(t)
	fs.completeSize = 20 * 1024 * 1024
	r := newUploadTestRouter()

	_, env := initUpload(t, r, "report.pdf", 20*1024*1024, "50")
	uploadID := env.Data["uploadId"].(string)
	key := env.Data["key"].(string)

	rec, env := doJSON(t, r, http.MethodPost, "/api/uploads/"+uploadID+"/complete", completeBody(1, 2, 3),
		map[string]string{"X-User-Plan-Max-File-MB": "50"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if env.Data["complete"] != true {
		t.Errorf("complete = %v, want true", env.Data["complete"])
	}
	if env.Data["fileName"] != "report.pdf" {
		t.Errorf("fileName = %v, want report.pdf", env.Data["fileName"])
	}
	if int64(env.Data["size"].(float64)) != fs.completeSize {
		t.Errorf("size = %v, want %d (the TRUE object size)", env.Data["size"], fs.completeSize)
	}
	if !fs.hasObject(fs.BucketUploads(), key) {
		t.Errorf("completed object must exist at %s/%s", fs.BucketUploads(), key)
	}
	if size, _ := client.HGet(context.Background(), "upload:"+uploadID, "size").Result(); size == "" {
		t.Error("redis state must record the verified size for job creation")
	}
}

func TestCompleteUpload_SessionExpired(t *testing.T) {
	withMiniRedis(t)
	withFakeStore(t)
	r := newUploadTestRouter()

	rec, env := doJSON(t, r, http.MethodPost, "/api/uploads/gone/complete", completeBody(1), nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if env.Error == nil || env.Error.Code != "NOT_FOUND" {
		t.Errorf("error code = %v, want NOT_FOUND", env.Error)
	}
}

func TestAbortUpload_AbortsAndClearsState(t *testing.T) {
	_, client := withMiniRedis(t)
	fs := withFakeStore(t)
	r := newUploadTestRouter()

	_, env := initUpload(t, r, "report.pdf", 20*1024*1024, "50")
	uploadID := env.Data["uploadId"].(string)

	rec, _ := doJSON(t, r, http.MethodDelete, "/api/uploads/"+uploadID, nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if len(fs.aborted) != 1 {
		t.Errorf("AbortMultipart calls = %d, want 1", len(fs.aborted))
	}
	if n, _ := client.Exists(context.Background(), "upload:"+uploadID).Result(); n != 0 {
		t.Error("redis state must be deleted after abort")
	}
}

func TestAbortUpload_IdempotentOnUnknownSession(t *testing.T) {
	withMiniRedis(t)
	withFakeStore(t)
	r := newUploadTestRouter()

	rec, _ := doJSON(t, r, http.MethodDelete, "/api/uploads/never-existed", nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (abort must be idempotent)", rec.Code)
	}
}

func TestGetUploadStatus(t *testing.T) {
	withMiniRedis(t)
	withFakeStore(t)
	r := newUploadTestRouter()

	_, env := initUpload(t, r, "report.pdf", 20*1024*1024, "50")
	uploadID := env.Data["uploadId"].(string)

	rec, env := doJSON(t, r, http.MethodGet, "/api/uploads/"+uploadID+"/status", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if env.Data["fileName"] != "report.pdf" {
		t.Errorf("fileName = %v, want report.pdf", env.Data["fileName"])
	}
	if int64(env.Data["declaredSize"].(float64)) != 20*1024*1024 {
		t.Errorf("declaredSize = %v, want %d", env.Data["declaredSize"], 20*1024*1024)
	}
	if int(env.Data["totalParts"].(float64)) != 3 {
		t.Errorf("totalParts = %v, want 3", env.Data["totalParts"])
	}
}

func TestUploadChunk_Returns410ProtocolChanged(t *testing.T) {
	r := newUploadTestRouter()

	rec, env := doJSON(t, r, http.MethodPut, "/api/uploads/any/chunk", nil, nil)
	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410", rec.Code)
	}
	if env.Error == nil || env.Error.Code != "UPLOAD_PROTOCOL_CHANGED" {
		t.Errorf("error code = %v, want UPLOAD_PROTOCOL_CHANGED", env.Error)
	}
	if env.Error != nil && !strings.Contains(env.Error.Details, "refresh the page") {
		t.Errorf("details = %q, want refresh-the-page guidance", env.Error.Details)
	}
}

func TestUploadPartSize(t *testing.T) {
	t.Run("default 8MiB", func(t *testing.T) {
		t.Setenv("UPLOAD_PART_SIZE_MB", "")
		if got := uploadPartSize(); got != 8*1024*1024 {
			t.Errorf("uploadPartSize() = %d, want %d", got, 8*1024*1024)
		}
	})
	t.Run("custom 16MiB", func(t *testing.T) {
		t.Setenv("UPLOAD_PART_SIZE_MB", "16")
		if got := uploadPartSize(); got != 16*1024*1024 {
			t.Errorf("uploadPartSize() = %d, want %d", got, 16*1024*1024)
		}
	})
	t.Run("clamped to the S3 5MiB minimum", func(t *testing.T) {
		t.Setenv("UPLOAD_PART_SIZE_MB", "1")
		if got := uploadPartSize(); got != 5*1024*1024 {
			t.Errorf("uploadPartSize() = %d, want %d", got, 5*1024*1024)
		}
	})
	t.Run("invalid uses default", func(t *testing.T) {
		t.Setenv("UPLOAD_PART_SIZE_MB", "abc")
		if got := uploadPartSize(); got != 8*1024*1024 {
			t.Errorf("uploadPartSize() = %d, want %d", got, 8*1024*1024)
		}
	})
}

func TestParsePartNumbers(t *testing.T) {
	t.Run("empty means all", func(t *testing.T) {
		got, err := parsePartNumbers("", 3)
		if err != nil || len(got) != 3 {
			t.Errorf("parsePartNumbers(\"\", 3) = %v, %v; want [1 2 3]", got, err)
		}
	})
	t.Run("subset", func(t *testing.T) {
		got, err := parsePartNumbers("2, 3", 3)
		if err != nil || len(got) != 2 || got[0] != 2 || got[1] != 3 {
			t.Errorf("parsePartNumbers(\"2, 3\", 3) = %v, %v", got, err)
		}
	})
	t.Run("out of range rejected", func(t *testing.T) {
		if _, err := parsePartNumbers("4", 3); err == nil {
			t.Error("expected error for part number beyond totalParts")
		}
	})
	t.Run("garbage rejected", func(t *testing.T) {
		if _, err := parsePartNumbers("a,b", 3); err == nil {
			t.Error("expected error for non-numeric part numbers")
		}
	})
}

func TestSanitizeFileName(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"report.pdf", "report.pdf"},
		{"  report.pdf  ", "report.pdf"},
		{"../../etc/passwd", "passwd"},
		{"/abs/path/file.pdf", "file.pdf"},
		{"..", ""},
		{".", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := sanitizeFileName(tt.in); got != tt.want {
			t.Errorf("sanitizeFileName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestUploadTTL(t *testing.T) {
	t.Run("default 30m", func(t *testing.T) {
		t.Setenv("UPLOAD_TTL", "")
		if got := uploadTTL(); got != 30*time.Minute {
			t.Errorf("uploadTTL() = %v, want 30m", got)
		}
	})
	t.Run("custom", func(t *testing.T) {
		t.Setenv("UPLOAD_TTL", "1h")
		if got := uploadTTL(); got != time.Hour {
			t.Errorf("uploadTTL() = %v, want 1h", got)
		}
	})
}

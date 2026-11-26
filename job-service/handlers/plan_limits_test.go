package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// planCtx builds a gin context carrying the gateway's plan headers. An empty
// value leaves that header out.
func planCtx(fileMB, filesN, jobMB string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	for k, v := range map[string]string{
		"X-User-Plan-Max-File-MB": fileMB,
		"X-User-Plan-Max-Files":   filesN,
		"X-User-Plan-Max-Job-MB":  jobMB,
	} {
		if v != "" {
			c.Request.Header.Set(k, v)
		}
	}
	return c
}

func TestPlanMaxJobInputMB(t *testing.T) {
	t.Setenv("GUEST_MAX_JOB_MB", "")
	cases := []struct {
		header string
		want   int
	}{
		{"300", 300},
		{"", 100},    // missing → guest fallback
		{"abc", 100}, // invalid → guest fallback
		{"0", 100},   // non-positive → guest fallback
	}
	for _, tc := range cases {
		if got := planMaxJobInputMB(planCtx("", "", tc.header)); got != tc.want {
			t.Errorf("header %q: got %d, want %d", tc.header, got, tc.want)
		}
	}
}

func TestPlanMaxOCRPages(t *testing.T) {
	t.Setenv("GUEST_MAX_OCR_PAGES", "")
	cases := []struct {
		header string
		want   int
	}{
		{"200", 200},
		{"", 10},    // missing → guest fallback
		{"abc", 10}, // invalid → guest fallback
		{"0", 10},   // non-positive → guest fallback, never unlimited
	}
	for _, tc := range cases {
		c := planCtx("", "", "")
		if tc.header != "" {
			c.Request.Header.Set("X-User-Plan-Max-Ocr-Pages", tc.header)
		}
		if got := planMaxOCRPages(c); got != tc.want {
			t.Errorf("header %q: got %d, want %d", tc.header, got, tc.want)
		}
	}
}

func TestEffectiveMaxFileMB(t *testing.T) {
	t.Setenv("MAX_UPLOAD_MB", "")
	cases := []struct {
		name          string
		fileMB, jobMB string
		tool          string
		wantMB        int
		wantScope     string
	}{
		{"plan per-file wins", "50", "300", "compress-pdf", 50, "your plan"},
		{"job total caps a larger per-file", "300", "100", "compress-pdf", 100, "your plan"},
		{"worker ceiling caps the plan", "500", "500", "compress-pdf", 300, "your plan"},
		{"image tool cap", "300", "300", "image-to-pdf", 20, "this tool"},
		{"scan tool cap", "50", "300", "scan-to-pdf", 20, "this tool"},
		{"legacy image alias", "50", "300", "img-to-pdf", 20, "this tool"},
		{"plan below tool cap keeps plan", "10", "300", "scan-to-pdf", 10, "your plan"},
		{"unknown tool at upload time", "300", "300", "", 300, "your plan"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mb, scope := effectiveMaxFileMB(planCtx(tc.fileMB, "", tc.jobMB), tc.tool)
			if mb != tc.wantMB || scope != tc.wantScope {
				t.Errorf("got %d/%q, want %d/%q", mb, scope, tc.wantMB, tc.wantScope)
			}
		})
	}
}

func TestEffectiveMaxJobMB(t *testing.T) {
	t.Setenv("MAX_UPLOAD_MB", "")
	if got := effectiveMaxJobMB(planCtx("", "", "250")); got != 250 {
		t.Errorf("plan below ceiling: got %d, want 250", got)
	}
	if got := effectiveMaxJobMB(planCtx("", "", "900")); got != 300 {
		t.Errorf("plan above ceiling: got %d, want the 300 ceiling", got)
	}
}

func TestInitUpload_PlanLimits(t *testing.T) {
	t.Setenv("MAX_UPLOAD_MB", "")
	const mb = int64(bytesPerMB)
	cases := []struct {
		name     string
		size     int64
		headers  map[string]string
		wantCode int
	}{
		{"pro exactly at 300MB", 300 * mb, map[string]string{"X-User-Plan-Max-File-MB": "300", "X-User-Plan-Max-Job-MB": "300"}, http.StatusCreated},
		{"pro one byte over 300MB", 300*mb + 1, map[string]string{"X-User-Plan-Max-File-MB": "300", "X-User-Plan-Max-Job-MB": "300"}, http.StatusRequestEntityTooLarge},
		{"plan row above ceiling still capped", 301 * mb, map[string]string{"X-User-Plan-Max-File-MB": "500", "X-User-Plan-Max-Job-MB": "500"}, http.StatusRequestEntityTooLarge},
		{"file larger than the job total", 101 * mb, map[string]string{"X-User-Plan-Max-File-MB": "300", "X-User-Plan-Max-Job-MB": "100"}, http.StatusRequestEntityTooLarge},
		{"missing job header falls back to guest total", 101 * mb, map[string]string{"X-User-Plan-Max-File-MB": "300"}, http.StatusRequestEntityTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withMiniRedis(t)
			fs := withFakeStore(t)
			r := newUploadTestRouter()
			rec, env := doJSON(t, r, http.MethodPost, "/api/uploads/init", UploadInitRequest{
				FileName: "big.pdf", FileSize: tc.size, ContentType: "application/pdf",
			}, tc.headers)
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantCode == http.StatusRequestEntityTooLarge {
				if env.Error == nil || env.Error.Code != "FILE_TOO_LARGE" {
					t.Errorf("error = %v, want FILE_TOO_LARGE", env.Error)
				}
				if len(fs.multiparts) != 0 {
					t.Errorf("a rejected upload must not allocate storage, got %d multiparts", len(fs.multiparts))
				}
			}
		})
	}
}

// createJobRouter mounts job creation for every tool group used below.
func createJobRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/optimize-pdf/:tool", CreateJobFromTool)
	r.POST("/api/organize-pdf/:tool", CreateJobFromTool)
	return r
}

// pdfOfSize returns a buffer that sniffs as application/pdf.
func pdfOfSize(n int) []byte {
	b := bytes.Repeat([]byte{' '}, n)
	copy(b, "%PDF-1.4\n")
	return b
}

func TestCreateJob_JSONPath_JobTotal(t *testing.T) {
	const kib = 1024
	headers := map[string]string{
		"X-User-Plan-Max-File-MB": "1",
		"X-User-Plan-Max-Files":   "10",
		"X-User-Plan-Max-Job-MB":  "1",
	}
	t.Run("over the job total is rejected before any job", func(t *testing.T) {
		_, client := withMiniRedis(t)
		fs := withFakeStore(t)
		a, b := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
		seedUploadObject(t, client, fs, a, "a.pdf", pdfOfSize(600*kib))
		seedUploadObject(t, client, fs, b, "b.pdf", pdfOfSize(600*kib))

		rec, env := doJSON(t, createJobRouter(), http.MethodPost, "/api/optimize-pdf/compress-pdf",
			map[string]any{"uploadIds": []string{a, b}}, headers)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413 (body %s)", rec.Code, rec.Body.String())
		}
		if env.Error == nil || env.Error.Code != "JOB_TOO_LARGE" {
			t.Fatalf("error = %v, want JOB_TOO_LARGE", env.Error)
		}
		// The uploads stay usable and unclaimed so the client can resubmit fewer.
		ctx := context.Background()
		for _, id := range []string{a, b} {
			if n, _ := client.Exists(ctx, "upload:"+id).Result(); n != 1 {
				t.Errorf("upload %s state removed by a rejected job", id)
			}
			if n, _ := client.Exists(ctx, uploadClaimKey(id)).Result(); n != 0 {
				t.Errorf("upload %s claim not released", id)
			}
		}
	})
	t.Run("tool cap applies to presigned uploads", func(t *testing.T) {
		_, client := withMiniRedis(t)
		fs := withFakeStore(t)
		id := "33333333-3333-4333-8333-333333333333"
		png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 21*bytesPerMB)...)
		seedUploadObject(t, client, fs, id, "photo.png", png)
		rec, env := doJSON(t, createJobRouter(), http.MethodPost, "/api/organize-pdf/scan-to-pdf",
			map[string]any{"uploadId": id}, map[string]string{
				"X-User-Plan-Max-File-MB": "300", "X-User-Plan-Max-Files": "50", "X-User-Plan-Max-Job-MB": "300",
			})
		if rec.Code != http.StatusRequestEntityTooLarge || env.Error == nil || env.Error.Code != "FILE_TOO_LARGE" {
			t.Fatalf("status = %d error = %v, want 413 FILE_TOO_LARGE (body %s)", rec.Code, env.Error, rec.Body.String())
		}
	})
}

// postMultipart submits files as the legacy direct-upload multipart form.
func postMultipart(t *testing.T, path string, files map[string][]byte, headers map[string]string) (*httptest.ResponseRecorder, apiEnvelope) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for name, data := range files {
		fw, err := w.CreateFormFile("files", name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write(data)
	}
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	createJobRouter().ServeHTTP(rec, req)
	var env apiEnvelope
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	return rec, env
}

func TestCreateJob_MultipartPath_Limits(t *testing.T) {
	const kib = 1024
	t.Run("over the job total stores nothing", func(t *testing.T) {
		withMiniRedis(t)
		fs := withFakeStore(t)
		rec, env := postMultipart(t, "/api/optimize-pdf/merge-pdf", map[string][]byte{
			"a.pdf": pdfOfSize(600 * kib), "b.pdf": pdfOfSize(600 * kib),
		}, map[string]string{"X-User-Plan-Max-File-MB": "1", "X-User-Plan-Max-Files": "10", "X-User-Plan-Max-Job-MB": "1"})
		if rec.Code != http.StatusRequestEntityTooLarge || env.Error == nil || env.Error.Code != "JOB_TOO_LARGE" {
			t.Fatalf("status = %d error = %v, want 413 JOB_TOO_LARGE (body %s)", rec.Code, env.Error, rec.Body.String())
		}
		if len(fs.objects) != 0 {
			t.Errorf("a rejected job must store no objects, got %d", len(fs.objects))
		}
	})
	t.Run("image over the tool cap", func(t *testing.T) {
		withMiniRedis(t)
		fs := withFakeStore(t)
		png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 21*bytesPerMB)...)
		rec, env := postMultipart(t, "/api/organize-pdf/scan-to-pdf", map[string][]byte{"photo.png": png},
			map[string]string{"X-User-Plan-Max-File-MB": "300", "X-User-Plan-Max-Files": "50", "X-User-Plan-Max-Job-MB": "300"})
		if rec.Code != http.StatusRequestEntityTooLarge || env.Error == nil || env.Error.Code != "FILE_TOO_LARGE" {
			t.Fatalf("status = %d error = %v, want 413 FILE_TOO_LARGE (body %s)", rec.Code, env.Error, rec.Body.String())
		}
		if len(fs.objects) != 0 {
			t.Errorf("a rejected job must store no objects, got %d", len(fs.objects))
		}
	})
}

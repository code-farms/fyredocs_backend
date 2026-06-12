package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/datatypes"

	"fyredocs/shared/redisstore"

	"job-service/internal/models"
)

// withMiniRedis swaps the global redisstore.Client for one backed by an
// in-process miniredis for the duration of the test, then restores the
// original. The returned client points at the test server so callers can
// inspect/seed state directly.
func withMiniRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	prev := redisstore.Client
	redisstore.Client = client
	t.Cleanup(func() { redisstore.Client = prev })
	return mr, client
}

// seedUploadObject creates the Redis session state and the uploaded object in
// the fake store, matching the state left behind by a successful InitUpload +
// CompleteUpload of the presigned multipart protocol.
func seedUploadObject(t *testing.T, client *redis.Client, fs *fakeStore, uploadID string, fileName string, contents []byte) string {
	t.Helper()
	key := fmt.Sprintf("uploads/%s/%s", uploadID, fileName)
	if err := client.HSet(context.Background(), "upload:"+uploadID, map[string]interface{}{
		"fileName":     fileName,
		"declaredSize": len(contents),
		"contentType":  "application/octet-stream",
		"bucket":       fs.BucketUploads(),
		"key":          key,
		"s3UploadId":   "mpu-seeded",
		"partSize":     8 * 1024 * 1024,
		"totalParts":   1,
		"size":         len(contents),
	}).Err(); err != nil {
		t.Fatalf("seed redis: %v", err)
	}
	fs.putObjectBytes(fs.BucketUploads(), key, contents)
	return key
}

func TestNormalizeToolType(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"ppt-to-pdf", "powerpoint-to-pdf", false},
		{"pdf-to-ppt", "pdf-to-powerpoint", false},
		{"pdf-to-img", "pdf-to-image", false},
		{"img-to-pdf", "image-to-pdf", false},
		{"word-to-pdf", "word-to-pdf", false},
		{"pdf-to-word", "pdf-to-word", false},
		{"compress-pdf", "compress-pdf", false},
		{"", "", true},
		{"  ", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := normalizeToolType(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("normalizeToolType(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("normalizeToolType(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidateFileType(t *testing.T) {
	tests := []struct {
		name     string
		toolType string
		fileName string
		wantErr  bool
	}{
		// PDF-only tools
		{"pdf-to-word accepts PDF", "pdf-to-word", "doc.pdf", false},
		{"pdf-to-word rejects Word", "pdf-to-word", "doc.docx", true},
		{"merge-pdf accepts PDF", "merge-pdf", "doc.pdf", false},
		{"compress-pdf accepts PDF", "compress-pdf", "doc.pdf", false},
		{"split-pdf accepts PDF", "split-pdf", "doc.pdf", false},
		{"ocr accepts PDF", "ocr", "doc.pdf", false},

		// Word tools
		{"word-to-pdf accepts .doc", "word-to-pdf", "file.doc", false},
		{"word-to-pdf accepts .docx", "word-to-pdf", "file.docx", false},
		{"word-to-pdf rejects .pdf", "word-to-pdf", "file.pdf", true},
		{"word-to-pdf rejects .txt", "word-to-pdf", "file.txt", true},

		// Excel tools
		{"excel-to-pdf accepts .xls", "excel-to-pdf", "file.xls", false},
		{"excel-to-pdf accepts .xlsx", "excel-to-pdf", "file.xlsx", false},
		{"excel-to-pdf rejects .csv", "excel-to-pdf", "file.csv", true},

		// PowerPoint tools
		{"ppt-to-pdf accepts .ppt", "powerpoint-to-pdf", "file.ppt", false},
		{"ppt-to-pdf accepts .pptx", "powerpoint-to-pdf", "file.pptx", false},
		{"ppt-to-pdf rejects .pdf", "powerpoint-to-pdf", "file.pdf", true},

		// Image tools
		{"image-to-pdf accepts .png", "image-to-pdf", "photo.png", false},
		{"image-to-pdf accepts .jpg", "image-to-pdf", "photo.jpg", false},
		{"image-to-pdf accepts .jpeg", "image-to-pdf", "photo.jpeg", false},
		{"image-to-pdf accepts .webp", "image-to-pdf", "photo.webp", false},
		{"image-to-pdf rejects .gif", "image-to-pdf", "photo.gif", true},
		{"image-to-pdf rejects .bmp", "image-to-pdf", "photo.bmp", true},

		// Previously missing PDF-only tools
		{"rotate-pdf accepts PDF", "rotate-pdf", "doc.pdf", false},
		{"rotate-pdf rejects Word", "rotate-pdf", "doc.docx", true},
		{"remove-pages accepts PDF", "remove-pages", "doc.pdf", false},
		{"extract-pages accepts PDF", "extract-pages", "doc.pdf", false},
		{"repair-pdf accepts PDF", "repair-pdf", "doc.pdf", false},
		{"ocr-pdf accepts PDF", "ocr-pdf", "doc.pdf", false},
		{"add-page-numbers accepts PDF", "add-page-numbers", "doc.pdf", false},
		{"organize-pdf accepts PDF", "organize-pdf", "doc.pdf", false},
		{"pdf-to-html accepts PDF", "pdf-to-html", "doc.pdf", false},
		{"pdf-to-text accepts PDF", "pdf-to-text", "doc.pdf", false},
		{"pdf-to-pdfa accepts PDF", "pdf-to-pdfa", "doc.pdf", false},

		// scan-to-pdf accepts images and PDF
		{"scan-to-pdf accepts .png", "scan-to-pdf", "photo.png", false},
		{"scan-to-pdf accepts .jpg", "scan-to-pdf", "photo.jpg", false},
		{"scan-to-pdf accepts .pdf", "scan-to-pdf", "doc.pdf", false},
		{"scan-to-pdf rejects .doc", "scan-to-pdf", "doc.doc", true},

		// LibreOffice PDF-to-ODF tools
		{"pdf-to-odt accepts PDF", "pdf-to-odt", "doc.pdf", false},
		{"pdf-to-odt rejects Word", "pdf-to-odt", "doc.docx", true},
		{"pdf-to-ods accepts PDF", "pdf-to-ods", "data.pdf", false},
		{"pdf-to-ods rejects Excel", "pdf-to-ods", "data.xlsx", true},
		{"pdf-to-odp accepts PDF", "pdf-to-odp", "slides.pdf", false},
		{"pdf-to-odp rejects PPT", "pdf-to-odp", "slides.pptx", true},

		// LibreOffice Office-to-ODF tools
		{"word-to-odt accepts .doc", "word-to-odt", "file.doc", false},
		{"word-to-odt accepts .docx", "word-to-odt", "file.docx", false},
		{"word-to-odt rejects .pdf", "word-to-odt", "file.pdf", true},
		{"excel-to-ods accepts .xls", "excel-to-ods", "file.xls", false},
		{"excel-to-ods accepts .xlsx", "excel-to-ods", "file.xlsx", false},
		{"excel-to-ods rejects .csv", "excel-to-ods", "file.csv", true},
		{"powerpoint-to-odp accepts .ppt", "powerpoint-to-odp", "file.ppt", false},
		{"powerpoint-to-odp accepts .pptx", "powerpoint-to-odp", "file.pptx", false},
		{"powerpoint-to-odp rejects .pdf", "powerpoint-to-odp", "file.pdf", true},

		// LibreOffice ODF-to-PDF tools
		{"odt-to-pdf accepts .odt", "odt-to-pdf", "file.odt", false},
		{"odt-to-pdf rejects .pdf", "odt-to-pdf", "file.pdf", true},
		{"odt-to-pdf rejects .docx", "odt-to-pdf", "file.docx", true},
		{"ods-to-pdf accepts .ods", "ods-to-pdf", "file.ods", false},
		{"ods-to-pdf rejects .xlsx", "ods-to-pdf", "file.xlsx", true},
		{"odp-to-pdf accepts .odp", "odp-to-pdf", "file.odp", false},
		{"odp-to-pdf rejects .pptx", "odp-to-pdf", "file.pptx", true},

		// Case insensitive extension
		{"accepts uppercase .PDF", "pdf-to-word", "doc.PDF", false},
		{"accepts uppercase .DOCX", "word-to-pdf", "file.DOCX", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFileType(tt.toolType, tt.fileName)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateFileType(%q, %q) error = %v, wantErr %v", tt.toolType, tt.fileName, err, tt.wantErr)
			}
		})
	}
}

func TestOutputFileName(t *testing.T) {
	tests := []struct {
		toolType    string
		inputName   string
		metadata    datatypes.JSON
		wantName    string
		wantType    string
	}{
		{"pdf-to-word", "report.pdf", nil, "report.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{"pdf-to-excel", "data.pdf", nil, "data.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
		{"pdf-to-powerpoint", "slides.pdf", nil, "slides.pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
		{"pdf-to-image", "doc.pdf", nil, "doc.zip", "application/zip"},
		{"pdf-to-image", "doc.pdf", datatypes.JSON(`{"outputExt":".png"}`), "doc.png", "image/png"},
		{"pdf-to-image", "doc.pdf", datatypes.JSON(`{"outputExt":".zip"}`), "doc.zip", "application/zip"},
		{"split-pdf", "doc.pdf", nil, "doc.zip", "application/zip"},
		{"word-to-pdf", "doc.docx", nil, "doc.pdf", "application/pdf"},
		{"image-to-pdf", "photo.jpeg", nil, "photo.pdf", "application/pdf"},
		{"compress-pdf", "doc.pdf", nil, "doc.pdf", "application/pdf"},
		{"merge-pdf", "doc.pdf", nil, "doc.pdf", "application/pdf"},
		{"pdf-to-html", "doc.pdf", nil, "doc.zip", "application/zip"},
		{"pdf-to-text", "doc.pdf", nil, "doc.txt", "text/plain; charset=utf-8"},
		{"pdf-to-odt", "report.pdf", nil, "report.odt", "application/vnd.oasis.opendocument.text"},
		{"word-to-odt", "report.docx", nil, "report.odt", "application/vnd.oasis.opendocument.text"},
		{"pdf-to-ods", "data.pdf", nil, "data.ods", "application/vnd.oasis.opendocument.spreadsheet"},
		{"excel-to-ods", "data.xlsx", nil, "data.ods", "application/vnd.oasis.opendocument.spreadsheet"},
		{"pdf-to-odp", "slides.pdf", nil, "slides.odp", "application/vnd.oasis.opendocument.presentation"},
		{"powerpoint-to-odp", "slides.pptx", nil, "slides.odp", "application/vnd.oasis.opendocument.presentation"},
		{"odt-to-pdf", "doc.odt", nil, "doc.pdf", "application/pdf"},
		{"ods-to-pdf", "data.ods", nil, "data.pdf", "application/pdf"},
		{"odp-to-pdf", "slides.odp", nil, "slides.pdf", "application/pdf"},
	}
	for _, tt := range tests {
		t.Run(tt.toolType+"_"+tt.inputName, func(t *testing.T) {
			gotName, gotType := outputFileName(tt.toolType, tt.inputName, tt.metadata)
			if gotName != tt.wantName {
				t.Errorf("outputFileName(%q, %q) name = %q, want %q", tt.toolType, tt.inputName, gotName, tt.wantName)
			}
			if gotType != tt.wantType {
				t.Errorf("outputFileName(%q, %q) type = %q, want %q", tt.toolType, tt.inputName, gotType, tt.wantType)
			}
		})
	}
}

func TestParseOptions(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"valid json", `{"key":"value","num":42}`, 2},
		{"empty string", "", 0},
		{"invalid json", "not-json", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseOptions(tt.input)
			if len(got) != tt.want {
				t.Errorf("parseOptions(%q) len = %d, want %d", tt.input, len(got), tt.want)
			}
		})
	}
}

func TestOptionsPayload(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantNil bool
	}{
		{"valid json", `{"key":"value"}`, false},
		{"empty string", "", true},
		{"invalid json", "not-json", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := optionsPayload(tt.input)
			if (got == nil) != tt.wantNil {
				t.Errorf("optionsPayload(%q) = %v, wantNil %v", tt.input, got, tt.wantNil)
			}
			if !tt.wantNil && !json.Valid(got) {
				t.Errorf("optionsPayload(%q) returned invalid JSON", tt.input)
			}
		})
	}
}

func TestClampInt(t *testing.T) {
	tests := []struct {
		value, min, max, want int
	}{
		{5, 1, 10, 5},
		{0, 1, 10, 1},
		{15, 1, 10, 10},
		{1, 1, 10, 1},
		{10, 1, 10, 10},
	}
	for _, tt := range tests {
		got := clampInt(tt.value, tt.min, tt.max)
		if got != tt.want {
			t.Errorf("clampInt(%d, %d, %d) = %d, want %d", tt.value, tt.min, tt.max, got, tt.want)
		}
	}
}

func TestMaxUploadBytes(t *testing.T) {
	t.Run("default 50MB", func(t *testing.T) {
		t.Setenv("MAX_UPLOAD_MB", "")
		got := maxUploadBytes()
		if got != 50*1024*1024 {
			t.Errorf("expected %d, got %d", 50*1024*1024, got)
		}
	})

	t.Run("custom 100MB", func(t *testing.T) {
		t.Setenv("MAX_UPLOAD_MB", "100")
		got := maxUploadBytes()
		if got != 100*1024*1024 {
			t.Errorf("expected %d, got %d", 100*1024*1024, got)
		}
	})

	t.Run("invalid uses default", func(t *testing.T) {
		t.Setenv("MAX_UPLOAD_MB", "abc")
		got := maxUploadBytes()
		if got != 50*1024*1024 {
			t.Errorf("expected %d, got %d", 50*1024*1024, got)
		}
	})

	t.Run("zero uses default", func(t *testing.T) {
		t.Setenv("MAX_UPLOAD_MB", "0")
		got := maxUploadBytes()
		if got != 50*1024*1024 {
			t.Errorf("expected %d, got %d", 50*1024*1024, got)
		}
	})
}

func TestGuestJobTTL(t *testing.T) {
	t.Run("default 30m", func(t *testing.T) {
		t.Setenv("GUEST_JOB_TTL", "")
		got := guestJobTTL()
		if got != 30*time.Minute {
			t.Errorf("expected 30m, got %v", got)
		}
	})

	t.Run("custom", func(t *testing.T) {
		t.Setenv("GUEST_JOB_TTL", "1h")
		got := guestJobTTL()
		if got != 1*time.Hour {
			t.Errorf("expected 1h, got %v", got)
		}
	})

	t.Run("invalid uses default", func(t *testing.T) {
		t.Setenv("GUEST_JOB_TTL", "invalid")
		got := guestJobTTL()
		if got != 30*time.Minute {
			t.Errorf("expected 30m, got %v", got)
		}
	})
}

func TestJobExpiry(t *testing.T) {
	t.Run("guest user gets GUEST_JOB_TTL expiry", func(t *testing.T) {
		t.Setenv("GUEST_JOB_TTL", "2h")
		got := jobExpiry(nil, "")
		if got == nil {
			t.Fatal("expected non-nil expiry for guest")
		}
		if time.Until(*got) < time.Hour || time.Until(*got) > 3*time.Hour {
			t.Errorf("expected expiry ~2h from now, got %v", *got)
		}
	})

	t.Run("free plan user gets FREE_JOB_TTL expiry", func(t *testing.T) {
		t.Setenv("FREE_JOB_TTL", "24h")
		uid := uuid.New()
		got := jobExpiry(&uid, "free")
		if got == nil {
			t.Fatal("expected non-nil expiry for free user")
		}
		if time.Until(*got) < 23*time.Hour || time.Until(*got) > 25*time.Hour {
			t.Errorf("expected expiry ~24h from now, got %v", *got)
		}
	})

	t.Run("empty plan name treated as free", func(t *testing.T) {
		t.Setenv("FREE_JOB_TTL", "24h")
		uid := uuid.New()
		got := jobExpiry(&uid, "")
		if got == nil {
			t.Fatal("expected non-nil expiry for empty plan user")
		}
	})

	t.Run("pro plan returns nil (never expires)", func(t *testing.T) {
		uid := uuid.New()
		got := jobExpiry(&uid, "pro")
		if got != nil {
			t.Errorf("expected nil expiry for pro user, got %v", got)
		}
	})
}

func TestFreeJobTTL(t *testing.T) {
	t.Run("default 24h", func(t *testing.T) {
		t.Setenv("FREE_JOB_TTL", "")
		got := freeJobTTL()
		if got != 24*time.Hour {
			t.Errorf("expected 24h, got %v", got)
		}
	})

	t.Run("custom", func(t *testing.T) {
		t.Setenv("FREE_JOB_TTL", "12h")
		got := freeJobTTL()
		if got != 12*time.Hour {
			t.Errorf("expected 12h, got %v", got)
		}
	})

	t.Run("invalid uses default", func(t *testing.T) {
		t.Setenv("FREE_JOB_TTL", "invalid")
		got := freeJobTTL()
		if got != 24*time.Hour {
			t.Errorf("expected 24h, got %v", got)
		}
	})
}

func TestMimeCategory(t *testing.T) {
	tests := []struct {
		toolType string
		want     string
	}{
		{"pdf-to-word", "pdf"},
		{"pdf-to-excel", "pdf"},
		{"merge-pdf", "pdf"},
		{"compress-pdf", "pdf"},
		{"ocr", "pdf"},
		{"rotate-pdf", "pdf"},
		{"remove-pages", "pdf"},
		{"extract-pages", "pdf"},
		{"repair-pdf", "pdf"},
		{"ocr-pdf", "pdf"},
		{"add-page-numbers", "pdf"},
		{"pdf-to-html", "pdf"},
		{"pdf-to-text", "pdf"},
		{"scan-to-pdf", "image"},
		{"word-to-pdf", "word"},
		{"excel-to-pdf", "excel"},
		{"powerpoint-to-pdf", "ppt"},
		{"image-to-pdf", "image"},
		{"pdf-to-odt", "pdf"},
		{"pdf-to-ods", "pdf"},
		{"pdf-to-odp", "pdf"},
		{"word-to-odt", "word"},
		{"excel-to-ods", "excel"},
		{"powerpoint-to-odp", "ppt"},
		{"odt-to-pdf", "odt"},
		{"ods-to-pdf", "ods"},
		{"odp-to-pdf", "odp"},
		{"unknown-tool", ""},
	}
	for _, tt := range tests {
		t.Run(tt.toolType, func(t *testing.T) {
			got := mimeCategory(tt.toolType)
			if got != tt.want {
				t.Errorf("mimeCategory(%q) = %q, want %q", tt.toolType, got, tt.want)
			}
		})
	}
}

func TestValidateMIMEHead(t *testing.T) {
	pdfHead := []byte("%PDF-1.4 fake content")
	pngHead := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	zipHead := []byte{0x50, 0x4B, 0x03, 0x04} // Office docs detected as zip
	txtHead := []byte("hello world plain text")

	tests := []struct {
		name     string
		toolType string
		head     []byte
		wantErr  bool
	}{
		{"PDF bytes for pdf-to-word", "pdf-to-word", pdfHead, false},
		{"PNG bytes for image-to-pdf", "image-to-pdf", pngHead, false},
		{"ZIP bytes for word-to-pdf (docx)", "word-to-pdf", zipHead, false},
		{"text bytes for pdf-to-word", "pdf-to-word", txtHead, true},
		{"PDF bytes for image-to-pdf", "image-to-pdf", pdfHead, true},
		{"unknown tool skips check", "unknown-tool", txtHead, false},
		{"oversized head is truncated to 512 bytes", "pdf-to-word", append([]byte("%PDF-1.4 "), make([]byte, 2048)...), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMIMEHead(tt.toolType, tt.head)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateMIMEHead(%q, ...) error = %v, wantErr %v", tt.toolType, err, tt.wantErr)
			}
		})
	}
}

func TestToJobResponse(t *testing.T) {
	tests := []struct {
		toolType       string
		inputFileName  string
		wantOutputName string
	}{
		{"image-to-pdf", "poem.jpeg", "poem.pdf"},
		{"image-to-pdf", "photo.png", "photo.pdf"},
		{"pdf-to-word", "report.pdf", "report.docx"},
		{"pdf-to-excel", "data.pdf", "data.xlsx"},
		{"pdf-to-image", "doc.pdf", "doc.zip"},
		{"compress-pdf", "file.pdf", "file.pdf"},
		{"word-to-pdf", "essay.docx", "essay.pdf"},
		{"pdf-to-odt", "report.pdf", "report.odt"},
		{"word-to-odt", "report.docx", "report.odt"},
		{"pdf-to-ods", "data.pdf", "data.ods"},
		{"excel-to-ods", "data.xlsx", "data.ods"},
		{"pdf-to-odp", "slides.pdf", "slides.odp"},
		{"powerpoint-to-odp", "slides.pptx", "slides.odp"},
		{"odt-to-pdf", "doc.odt", "doc.pdf"},
		{"ods-to-pdf", "data.ods", "data.pdf"},
		{"odp-to-pdf", "slides.odp", "slides.pdf"},
	}
	for _, tt := range tests {
		t.Run(tt.toolType+"_"+tt.inputFileName, func(t *testing.T) {
			job := models.ProcessingJob{
				ID:       uuid.New(),
				ToolType: tt.toolType,
				FileName: tt.inputFileName,
			}
			resp := toJobResponse(job)
			if resp.OutputFileName != tt.wantOutputName {
				t.Errorf("toJobResponse() OutputFileName = %q, want %q", resp.OutputFileName, tt.wantOutputName)
			}
			if resp.FileName != tt.inputFileName {
				t.Errorf("toJobResponse() FileName = %q, want %q (original preserved)", resp.FileName, tt.inputFileName)
			}
		})
	}
}

func TestToJobResponses(t *testing.T) {
	jobs := []models.ProcessingJob{
		{ID: uuid.New(), ToolType: "image-to-pdf", FileName: "a.jpeg"},
		{ID: uuid.New(), ToolType: "pdf-to-word", FileName: "b.pdf"},
	}
	responses := toJobResponses(jobs)
	if len(responses) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(responses))
	}
	if responses[0].OutputFileName != "a.pdf" {
		t.Errorf("responses[0].OutputFileName = %q, want %q", responses[0].OutputFileName, "a.pdf")
	}
	if responses[1].OutputFileName != "b.docx" {
		t.Errorf("responses[1].OutputFileName = %q, want %q", responses[1].OutputFileName, "b.docx")
	}
}

func TestToJobResponseJSON(t *testing.T) {
	job := models.ProcessingJob{
		ID:       uuid.New(),
		ToolType: "image-to-pdf",
		FileName: "photo.jpg",
		Status:   "completed",
	}
	resp := toJobResponse(job)
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m["outputFileName"] != "photo.pdf" {
		t.Errorf("JSON outputFileName = %v, want %q", m["outputFileName"], "photo.pdf")
	}
	if m["fileName"] != "photo.jpg" {
		t.Errorf("JSON fileName = %v, want %q (original preserved)", m["fileName"], "photo.jpg")
	}
}

func TestCreateJobResponseJSON(t *testing.T) {
	job := models.ProcessingJob{
		ID:       uuid.New(),
		ToolType: "edit-pdf",
		FileName: "doc.pdf",
		Status:   "queued",
	}

	t.Run("includes guestToken when set", func(t *testing.T) {
		resp := createJobResponse{
			jobResponse: toJobResponse(job),
			GuestToken:  "tok-abc-123",
		}
		data, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]interface{}
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		if m["guestToken"] != "tok-abc-123" {
			t.Errorf("guestToken = %v, want %q", m["guestToken"], "tok-abc-123")
		}
		if m["outputFileName"] != "doc.pdf" {
			t.Errorf("outputFileName = %v, want %q", m["outputFileName"], "doc.pdf")
		}
	})

	t.Run("omits guestToken when empty", func(t *testing.T) {
		resp := createJobResponse{
			jobResponse: toJobResponse(job),
			GuestToken:  "",
		}
		data, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]interface{}
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		if _, exists := m["guestToken"]; exists {
			t.Errorf("guestToken should be omitted when empty, got %v", m["guestToken"])
		}
	})
}

func TestOutputFileCacheStoreAndLoad(t *testing.T) {
	// Reset cache state for test isolation.
	outputFileCache.Range(func(key, value any) bool {
		outputFileCache.Delete(key)
		return true
	})

	jobID := uuid.New()
	entry := models.FileMetadata{
		JobID:        jobID,
		Kind:         "output",
		OriginalName: "test.png",
		Path:         "/app/outputs/processed_test.png",
	}

	// Cache miss initially.
	if _, ok := outputFileCache.Load(jobID); ok {
		t.Fatal("expected cache miss for new jobID")
	}

	// Store and retrieve.
	outputFileCache.Store(jobID, entry)
	cached, ok := outputFileCache.Load(jobID)
	if !ok {
		t.Fatal("expected cache hit after store")
	}
	got := cached.(models.FileMetadata)
	if got.Path != entry.Path {
		t.Errorf("cached Path = %q, want %q", got.Path, entry.Path)
	}
	if got.OriginalName != entry.OriginalName {
		t.Errorf("cached OriginalName = %q, want %q", got.OriginalName, entry.OriginalName)
	}
}

// pngBytes is a minimal PNG header that http.DetectContentType recognises.
var pngBytes = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D}

func TestConsumeUpload_HappyPathPreservesState(t *testing.T) {
	_, client := withMiniRedis(t)
	fs := withFakeStore(t)

	uploadID := "upl-success"
	key := seedUploadObject(t, client, fs, uploadID, "photo.png", pngBytes)

	consumed, err := consumeUpload(context.Background(), "image-to-pdf", uploadID)
	if err != nil {
		t.Fatalf("consumeUpload: %v", err)
	}
	if consumed.Key != key {
		t.Errorf("Key = %q, want %q", consumed.Key, key)
	}
	if consumed.OriginalName != "photo.png" {
		t.Errorf("OriginalName = %q, want photo.png", consumed.OriginalName)
	}
	if consumed.Size != int64(len(pngBytes)) {
		t.Errorf("Size = %d, want %d (true object size from StatObject)", consumed.Size, len(pngBytes))
	}

	// The object and Redis state must be untouched so the request can retry
	// the rest of the flow (DB tx, queue publish) without re-uploading.
	if !fs.hasObject(fs.BucketUploads(), key) {
		t.Error("object must be preserved after consumeUpload")
	}
	exists, err := client.Exists(context.Background(), "upload:"+uploadID).Result()
	if err != nil {
		t.Fatal(err)
	}
	if exists != 1 {
		t.Error("redis upload state must be preserved after consumeUpload")
	}
}

func TestConsumeUpload_RetrySafe(t *testing.T) {
	_, client := withMiniRedis(t)
	fs := withFakeStore(t)

	uploadID := "upl-retry"
	seedUploadObject(t, client, fs, uploadID, "photo.png", pngBytes)

	// consumeUpload is read-only — a second call with the same uploadId
	// (frontend retry after a downstream failure) must succeed identically.
	first, err := consumeUpload(context.Background(), "image-to-pdf", uploadID)
	if err != nil {
		t.Fatalf("first consumeUpload: %v", err)
	}
	second, err := consumeUpload(context.Background(), "image-to-pdf", uploadID)
	if err != nil {
		t.Fatalf("retry consumeUpload must succeed, got: %v", err)
	}
	if first != second {
		t.Errorf("retry must return identical result: %+v vs %+v", first, second)
	}
}

func TestConsumeUpload_RejectsMissingUpload(t *testing.T) {
	_, _ = withMiniRedis(t)
	withFakeStore(t)

	_, err := consumeUpload(context.Background(), "image-to-pdf", "nope")
	if err == nil || err.Error() != "upload not found" {
		t.Errorf("expected 'upload not found', got %v", err)
	}
}

func TestConsumeUpload_RejectsMissingObject(t *testing.T) {
	_, client := withMiniRedis(t)
	fs := withFakeStore(t)

	// Redis session exists but the object was never completed/uploaded.
	uploadID := "upl-no-object"
	key := seedUploadObject(t, client, fs, uploadID, "photo.png", pngBytes)
	if err := fs.RemoveObject(context.Background(), fs.BucketUploads(), key); err != nil {
		t.Fatal(err)
	}

	_, err := consumeUpload(context.Background(), "image-to-pdf", uploadID)
	if err == nil {
		t.Fatal("expected error when the uploaded object is missing")
	}
	// State must be preserved so the client can finish the upload and retry.
	exists, _ := client.Exists(context.Background(), "upload:"+uploadID).Result()
	if exists != 1 {
		t.Error("redis state must be preserved when the object is missing")
	}
}

func TestConsumeUpload_RejectsWrongFileType(t *testing.T) {
	_, client := withMiniRedis(t)
	fs := withFakeStore(t)

	uploadID := "upl-bad-ext"
	key := seedUploadObject(t, client, fs, uploadID, "doc.pdf", []byte("%PDF-1.4"))

	_, err := consumeUpload(context.Background(), "image-to-pdf", uploadID)
	if err == nil {
		t.Fatal("expected error for pdf submitted to image-to-pdf")
	}
	// Object and state must still be preserved on validation failure.
	if !fs.hasObject(fs.BucketUploads(), key) {
		t.Error("object must be preserved on validation failure")
	}
	exists, _ := client.Exists(context.Background(), "upload:"+uploadID).Result()
	if exists != 1 {
		t.Error("redis state must be preserved on validation failure")
	}
}

func TestConsumeUpload_RejectsMIMEMismatch(t *testing.T) {
	_, client := withMiniRedis(t)
	fs := withFakeStore(t)

	// Extension says PDF but the bytes are PNG — sniffed via GetObjectRange.
	uploadID := "upl-bad-mime"
	seedUploadObject(t, client, fs, uploadID, "doc.pdf", pngBytes)

	_, err := consumeUpload(context.Background(), "pdf-to-word", uploadID)
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("expected MIME mismatch error, got %v", err)
	}
}

func TestReleaseUpload_ClearsRedisStateOnly(t *testing.T) {
	_, client := withMiniRedis(t)
	fs := withFakeStore(t)

	uploadID := "upl-release"
	key := seedUploadObject(t, client, fs, uploadID, "photo.png", pngBytes)
	// Also seed the legacy chunks set so the release covers both keys.
	if err := client.SAdd(context.Background(), "upload:"+uploadID+":chunks", "0").Err(); err != nil {
		t.Fatal(err)
	}

	releaseUpload(context.Background(), uploadID)

	for _, k := range []string{"upload:" + uploadID, "upload:" + uploadID + ":chunks"} {
		exists, err := client.Exists(context.Background(), k).Result()
		if err != nil {
			t.Fatal(err)
		}
		if exists != 0 {
			t.Errorf("redis key %q must be cleared after releaseUpload", k)
		}
	}
	// The object must remain — workers still need to read it.
	if !fs.hasObject(fs.BucketUploads(), key) {
		t.Error("releaseUpload must not remove the uploaded object")
	}
}

func TestReleaseUpload_NoopOnEmptyOrMissing(t *testing.T) {
	_, _ = withMiniRedis(t)
	withFakeStore(t)

	// Must not panic on empty id or on a non-existent upload.
	releaseUpload(context.Background(), "")
	releaseUpload(context.Background(), "does-not-exist")
}

// makeFileHeader builds a real *multipart.FileHeader by round-tripping a
// multipart body through http.Request parsing.
func makeFileHeader(t *testing.T, name string, contents []byte) *multipart.FileHeader {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("files", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if err := req.ParseMultipartForm(32 << 20); err != nil {
		t.Fatal(err)
	}
	return req.MultipartForm.File["files"][0]
}

func TestStoreDirectUpload_HappyPath(t *testing.T) {
	fs := withFakeStore(t)

	contents := []byte("%PDF-1.4 direct upload contents")
	fh := makeFileHeader(t, "report.pdf", contents)

	key, err := storeDirectUpload(context.Background(), "pdf-to-word", "job-123", fh)
	if err != nil {
		t.Fatalf("storeDirectUpload: %v", err)
	}
	if key != "uploads/job-123/report.pdf" {
		t.Errorf("key = %q, want uploads/job-123/report.pdf", key)
	}
	got, err := fs.GetObjectRange(context.Background(), fs.BucketUploads(), key, 0, int64(len(contents)))
	if err != nil {
		t.Fatalf("stored object missing: %v", err)
	}
	if string(got) != string(contents) {
		t.Errorf("stored bytes = %q, want %q (sniffed head must be re-prepended)", got, contents)
	}
}

func TestStoreDirectUpload_RejectsMIMEMismatchAsInvalidInput(t *testing.T) {
	fs := withFakeStore(t)

	fh := makeFileHeader(t, "doc.pdf", []byte("plain text, not a pdf"))
	_, err := storeDirectUpload(context.Background(), "pdf-to-word", "job-123", fh)
	if err == nil {
		t.Fatal("expected MIME mismatch error")
	}
	if !errors.As(err, new(invalidInputError)) {
		t.Errorf("error must be an invalidInputError (maps to 400), got %T: %v", err, err)
	}
	if len(fs.objects) != 0 {
		t.Errorf("nothing may be stored on MIME rejection, got %v", fs.objects)
	}
}

func TestBucketFor(t *testing.T) {
	fs := withFakeStore(t)
	if got := bucketFor("input"); got != fs.BucketUploads() {
		t.Errorf("bucketFor(input) = %q, want %q", got, fs.BucketUploads())
	}
	if got := bucketFor("output"); got != fs.BucketOutputs() {
		t.Errorf("bucketFor(output) = %q, want %q", got, fs.BucketOutputs())
	}
}

func TestRedirectToOutput_Presigned302(t *testing.T) {
	fs := withFakeStore(t)
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/convert-from-pdf/pdf-to-word/j1/download", nil)

	redirectToOutput(c, "outputs/job-1/result.docx", "report.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (body %s)", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, fs.BucketOutputs()+"/outputs/job-1/result.docx") {
		t.Errorf("Location %q must address the outputs bucket and object key", loc)
	}
	if !strings.Contains(loc, "response-content-disposition=attachment") || !strings.Contains(loc, "report.docx") {
		t.Errorf("Location %q must force attachment disposition with the output file name", loc)
	}
	if !strings.Contains(loc, "response-content-type=") {
		t.Errorf("Location %q must override the response content type", loc)
	}
}

func TestRedirectToOutput_LegacyDiskPath404(t *testing.T) {
	withFakeStore(t)
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/convert-from-pdf/pdf-to-word/j1/download", nil)

	redirectToOutput(c, "/app/outputs/job-1/result.docx", "report.docx", "application/pdf")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for legacy disk paths", rec.Code)
	}
	var env apiEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if env.Error == nil || env.Error.Code != "NOT_FOUND" {
		t.Errorf("error code = %v, want NOT_FOUND", env.Error)
	}
	if env.Error != nil && !strings.Contains(env.Error.Details, "expired") {
		t.Errorf("details = %q, want download-link-expired message", env.Error.Details)
	}
}

func TestRecordConsumedUpload_WritesMappingWithTTL(t *testing.T) {
	mr, client := withMiniRedis(t)
	uploadID := "upl-x"
	jobID := uuid.NewString()

	recordConsumedUpload(context.Background(), uploadID, jobID)

	got, err := client.Get(context.Background(), consumedUploadKey(uploadID)).Result()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != jobID {
		t.Errorf("value = %q, want %q", got, jobID)
	}
	ttl := mr.TTL(consumedUploadKey(uploadID))
	if ttl <= 0 || ttl > consumedUploadIdempotencyTTL {
		t.Errorf("ttl = %v, want (0, %v]", ttl, consumedUploadIdempotencyTTL)
	}
}

func TestRecordConsumedUpload_NoopOnEmptyArgs(t *testing.T) {
	_, client := withMiniRedis(t)

	recordConsumedUpload(context.Background(), "", "any")
	recordConsumedUpload(context.Background(), "any", "")

	keys, err := client.Keys(context.Background(), "idempotency:upload:*").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Errorf("no keys should be written for empty args, got %v", keys)
	}
}

func TestFindExistingJobForUploads_MissOnEmptyOrPartial(t *testing.T) {
	_, client := withMiniRedis(t)

	// Empty input
	if job, ok := findExistingJobForUploads(context.Background(), nil); ok || job != nil {
		t.Errorf("empty uploadIds: ok=%v job=%v, want (false, nil)", ok, job)
	}
	// Partial: one mapping seeded, the other missing
	if err := client.Set(context.Background(), consumedUploadKey("a"), uuid.NewString(), time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if job, ok := findExistingJobForUploads(context.Background(), []string{"a", "b"}); ok || job != nil {
		t.Errorf("partial mapping: ok=%v job=%v, want (false, nil) — must not silently return a partial match", ok, job)
	}
}

func TestFindExistingJobForUploads_MissOnInconsistentJobIDs(t *testing.T) {
	_, client := withMiniRedis(t)

	// Two uploadIds map to DIFFERENT jobIds — must NOT return either, even if
	// both jobs exist. Returning the first would silently merge unrelated work.
	if err := client.Set(context.Background(), consumedUploadKey("a"), uuid.NewString(), time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(context.Background(), consumedUploadKey("b"), uuid.NewString(), time.Minute).Err(); err != nil {
		t.Fatal(err)
	}

	if job, ok := findExistingJobForUploads(context.Background(), []string{"a", "b"}); ok || job != nil {
		t.Errorf("inconsistent jobIds: ok=%v job=%v, want (false, nil)", ok, job)
	}
}

func TestFindExistingJobForUploads_MissOnEmptyUploadID(t *testing.T) {
	_, _ = withMiniRedis(t)

	// An empty string in the slice must short-circuit to miss — never let
	// `idempotency:upload:` (no id) act as a wildcard match.
	if job, ok := findExistingJobForUploads(context.Background(), []string{""}); ok || job != nil {
		t.Errorf("empty uploadId in slice: ok=%v job=%v, want (false, nil)", ok, job)
	}
}

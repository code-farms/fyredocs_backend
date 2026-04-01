package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"

	"job-service/internal/models"
)

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

func TestUniqueUploadFileName(t *testing.T) {
	tests := []struct {
		uploadID string
		fileName string
		index    int
		want     string
	}{
		{"abc-123", "document.pdf", 0, "abc-123_0_document.pdf"},
		{"abc-123", "image.png", 2, "abc-123_2_image.png"},
		{"", "document.pdf", 0, "document.pdf"},
	}
	for _, tt := range tests {
		got := uniqueUploadFileName(tt.uploadID, tt.fileName, tt.index)
		if got != tt.want {
			t.Errorf("uniqueUploadFileName(%q, %q, %d) = %q, want %q", tt.uploadID, tt.fileName, tt.index, got, tt.want)
		}
	}
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

func TestValidateMIMEType(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a fake PDF file (starts with %PDF)
	pdfPath := filepath.Join(tmpDir, "test.pdf")
	if err := os.WriteFile(pdfPath, []byte("%PDF-1.4 fake content"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a PNG file (starts with PNG magic bytes)
	pngPath := filepath.Join(tmpDir, "test.png")
	pngHeader := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	if err := os.WriteFile(pngPath, pngHeader, 0644); err != nil {
		t.Fatal(err)
	}

	// Create a ZIP file (Office docs detected as zip)
	zipPath := filepath.Join(tmpDir, "test.docx")
	zipHeader := []byte{0x50, 0x4B, 0x03, 0x04}
	if err := os.WriteFile(zipPath, zipHeader, 0644); err != nil {
		t.Fatal(err)
	}

	// Create a plain text file (wrong type for PDF tools)
	txtPath := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(txtPath, []byte("hello world plain text"), 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		toolType string
		filePath string
		wantErr  bool
	}{
		{"PDF file for pdf-to-word", "pdf-to-word", pdfPath, false},
		{"PNG file for image-to-pdf", "image-to-pdf", pngPath, false},
		{"ZIP file for word-to-pdf (docx)", "word-to-pdf", zipPath, false},
		{"text file for pdf-to-word", "pdf-to-word", txtPath, true},
		{"PDF file for image-to-pdf", "image-to-pdf", pdfPath, true},
		{"unknown tool skips check", "unknown-tool", txtPath, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMIMEType(tt.toolType, tt.filePath)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateMIMEType(%q, %q) error = %v, wantErr %v", tt.toolType, tt.filePath, err, tt.wantErr)
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

func TestValidateMIMETypeNonexistentFile(t *testing.T) {
	err := validateMIMEType("pdf-to-word", "/nonexistent/file.pdf")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

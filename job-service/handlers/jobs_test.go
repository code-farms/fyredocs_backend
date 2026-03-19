package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
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
		wantName    string
		wantType    string
	}{
		{"pdf-to-word", "report.pdf", "report.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{"pdf-to-excel", "data.pdf", "data.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
		{"pdf-to-powerpoint", "slides.pdf", "slides.pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
		{"pdf-to-image", "doc.pdf", "doc.zip", "application/zip"},
		{"split-pdf", "doc.pdf", "doc.zip", "application/zip"},
		{"word-to-pdf", "doc.docx", "doc.pdf", "application/pdf"},
		{"compress-pdf", "doc.pdf", "doc.pdf", "application/pdf"},
		{"merge-pdf", "doc.pdf", "doc.pdf", "application/pdf"},
	}
	for _, tt := range tests {
		t.Run(tt.toolType, func(t *testing.T) {
			gotName, gotType := outputFileName(tt.toolType, tt.inputName)
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
	t.Run("default 2h", func(t *testing.T) {
		t.Setenv("GUEST_JOB_TTL", "")
		got := guestJobTTL()
		if got != 2*time.Hour {
			t.Errorf("expected 2h, got %v", got)
		}
	})

	t.Run("custom", func(t *testing.T) {
		t.Setenv("GUEST_JOB_TTL", "30m")
		got := guestJobTTL()
		if got != 30*time.Minute {
			t.Errorf("expected 30m, got %v", got)
		}
	})

	t.Run("invalid uses default", func(t *testing.T) {
		t.Setenv("GUEST_JOB_TTL", "invalid")
		got := guestJobTTL()
		if got != 2*time.Hour {
			t.Errorf("expected 2h, got %v", got)
		}
	})
}

func TestGuestExpiry(t *testing.T) {
	t.Run("nil userID sets expiry", func(t *testing.T) {
		t.Setenv("GUEST_JOB_TTL", "2h")
		got := guestExpiry(nil)
		if got == nil {
			t.Fatal("expected non-nil expiry for guest")
		}
		if time.Until(*got) < time.Hour || time.Until(*got) > 3*time.Hour {
			t.Errorf("expected expiry ~2h from now, got %v", *got)
		}
	})

	t.Run("non-nil userID returns nil", func(t *testing.T) {
		uid := uuid.New()
		got := guestExpiry(&uid)
		if got != nil {
			t.Errorf("expected nil expiry for authenticated user, got %v", got)
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
		{"word-to-pdf", "word"},
		{"excel-to-pdf", "excel"},
		{"powerpoint-to-pdf", "ppt"},
		{"image-to-pdf", "image"},
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

func TestValidateMIMETypeNonexistentFile(t *testing.T) {
	err := validateMIMEType("pdf-to-word", "/nonexistent/file.pdf")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

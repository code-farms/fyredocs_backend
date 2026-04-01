package processing

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
)

func TestProcessFileNoInputs(t *testing.T) {
	_, err := ProcessFile(context.Background(), uuid.New(), "word-to-pdf", nil, nil, "", nil)
	if err == nil {
		t.Error("expected error for no input files")
	}
}

func TestProcessFileUnsupportedTool(t *testing.T) {
	_, err := ProcessFile(context.Background(), uuid.New(), "unknown-tool", []string{"/tmp/test.pdf"}, nil, t.TempDir(), nil)
	if err == nil {
		t.Error("expected error for unsupported tool")
	}
}

func TestProcessFileRecognizesODFTools(t *testing.T) {
	tools := []struct {
		name  string
		input string
	}{
		{"odt-to-pdf", "/nonexistent.odt"},
		{"ods-to-pdf", "/nonexistent.ods"},
		{"odp-to-pdf", "/nonexistent.odp"},
		{"word-to-odt", "/nonexistent.docx"},
		{"excel-to-ods", "/nonexistent.xlsx"},
		{"powerpoint-to-odp", "/nonexistent.pptx"},
	}
	for _, tt := range tools {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ProcessFile(context.Background(), uuid.New(), tt.name, []string{tt.input}, nil, t.TempDir(), nil)
			// Should fail on conversion (no LibreOffice), NOT on "unsupported tool type"
			if err != nil && err.Error() == "unsupported tool type: "+tt.name {
				t.Errorf("tool %q should be recognized but got unsupported error", tt.name)
			}
		})
	}
}

func TestOptionString(t *testing.T) {
	opts := map[string]interface{}{"key": "value", "empty": ""}
	val, ok := optionString(opts, "key")
	if !ok || val != "value" {
		t.Errorf("expected 'value', got %q ok=%v", val, ok)
	}
	_, ok = optionString(opts, "empty")
	if ok {
		t.Error("expected !ok for empty string")
	}
	_, ok = optionString(opts, "missing")
	if ok {
		t.Error("expected !ok for missing key")
	}
	_, ok = optionString(nil, "key")
	if ok {
		t.Error("expected !ok for nil options")
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := dir + "/src.txt"
	dst := dir + "/dst.txt"
	if err := os.WriteFile(src, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Errorf("copied content = %q, want %q", string(data), "hello")
	}
}

func TestCopyFileMissingSource(t *testing.T) {
	dir := t.TempDir()
	err := copyFile(dir+"/nonexistent.txt", dir+"/dst.txt")
	if err == nil {
		t.Error("expected error for nonexistent source")
	}
}

func TestProcessFileEmptyOutputDir(t *testing.T) {
	_, err := ProcessFile(context.Background(), uuid.New(), "word-to-pdf", []string{"/nonexistent/file.docx"}, nil, "", nil)
	if err == nil {
		t.Error("expected error for nonexistent input file")
	}
}

func TestOptionStringWithJsonNumber(t *testing.T) {
	opts := map[string]interface{}{"quality": 90}
	_, ok := optionString(opts, "quality")
	if !ok {
		t.Log("numeric option not extracted as string (expected behavior depends on implementation)")
	}
}

func TestEnvOrDefault(t *testing.T) {
	t.Run("returns env value when set", func(t *testing.T) {
		t.Setenv("TEST_ENVORD_KEY", "custom")
		got := envOrDefault("TEST_ENVORD_KEY", "fallback")
		if got != "custom" {
			t.Errorf("expected 'custom', got %q", got)
		}
	})

	t.Run("returns default when unset", func(t *testing.T) {
		t.Setenv("TEST_ENVORD_KEY", "")
		got := envOrDefault("TEST_ENVORD_KEY", "fallback")
		if got != "fallback" {
			t.Errorf("expected 'fallback', got %q", got)
		}
	})
}

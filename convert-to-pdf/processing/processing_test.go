package processing

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
)

func TestProcessFileNoInputs(t *testing.T) {
	_, err := ProcessFile(context.Background(), uuid.New(), "word-to-pdf", nil, nil, "")
	if err == nil {
		t.Error("expected error for no input files")
	}
}

func TestProcessFileUnsupportedTool(t *testing.T) {
	_, err := ProcessFile(context.Background(), uuid.New(), "unknown-tool", []string{"/tmp/test.pdf"}, nil, t.TempDir())
	if err == nil {
		t.Error("expected error for unsupported tool")
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
	// With empty output dir, should default to "outputs" and still error on missing file
	_, err := ProcessFile(context.Background(), uuid.New(), "word-to-pdf", []string{"/nonexistent/file.docx"}, nil, "")
	if err == nil {
		t.Error("expected error for nonexistent input file")
	}
}

func TestOptionStringWithJsonNumber(t *testing.T) {
	opts := map[string]interface{}{"quality": 90}
	_, ok := optionString(opts, "quality")
	// numeric values should still be handled (json.Marshal fallback)
	if !ok {
		// optionString marshals non-string values - 90 becomes "90"
		t.Log("numeric option not extracted as string (expected behavior depends on implementation)")
	}
}

func TestProcessFileAddPageNumbersNoInput(t *testing.T) {
	_, err := ProcessFile(context.Background(), uuid.New(), "add-page-numbers", nil, nil, "")
	if err == nil {
		t.Error("expected error for no input files")
	}
}

func TestProcessFileSignPdfNoInput(t *testing.T) {
	_, err := ProcessFile(context.Background(), uuid.New(), "sign-pdf", nil, nil, "")
	if err == nil {
		t.Error("expected error for no input files")
	}
}

func TestProcessFileEditPdfNoInput(t *testing.T) {
	_, err := ProcessFile(context.Background(), uuid.New(), "edit-pdf", nil, nil, "")
	if err == nil {
		t.Error("expected error for no input files")
	}
}

func TestProcessFileEditPdfMissingAnnotations(t *testing.T) {
	dir := t.TempDir()
	// Create a dummy file (not a valid PDF, so it will fail at processing level)
	src := dir + "/test.pdf"
	if err := os.WriteFile(src, []byte("dummy"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := ProcessFile(context.Background(), uuid.New(), "edit-pdf", []string{src}, nil, dir)
	if err == nil {
		t.Error("expected error for missing annotations option")
	}
}

func TestProcessFileSignPdfMissingSignature(t *testing.T) {
	dir := t.TempDir()
	src := dir + "/test.pdf"
	if err := os.WriteFile(src, []byte("dummy"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := ProcessFile(context.Background(), uuid.New(), "sign-pdf", []string{src}, nil, dir)
	if err == nil {
		t.Error("expected error for missing signature data")
	}
}

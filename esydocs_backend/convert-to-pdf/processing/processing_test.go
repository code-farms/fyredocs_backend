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

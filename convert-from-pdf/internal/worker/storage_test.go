package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyredocs/shared/storage"
)

// fakeStorage implements the Storage interface for tests. It records
// download/upload calls and supports error injection.
type fakeStorage struct {
	downloads     []string // "bucket/key"
	uploads       []string // "bucket/key"
	copies        []string // "srcBucket/srcKey->dstBucket/dstKey"
	contentType   map[string]string
	etagByKey     map[string]string // "bucket/key" -> ETag
	defaultETag   string
	downloadErr   error
	uploadErr     error
	uploadSize    int64
	objectSize    int64
	statErr       error
	statObjectErr error
	copyErr       error
}

func newFakeStorage() *fakeStorage {
	return &fakeStorage{contentType: map[string]string{}, etagByKey: map[string]string{}, defaultETag: "etag-default", uploadSize: 42, objectSize: 1024}
}

func (f *fakeStorage) BucketUploads() string { return "test-uploads" }
func (f *fakeStorage) BucketOutputs() string { return "test-outputs" }

func (f *fakeStorage) GetObjectSize(ctx context.Context, bucket, key string) (int64, error) {
	if f.statErr != nil {
		return 0, f.statErr
	}
	return f.objectSize, nil
}

func (f *fakeStorage) StatObject(ctx context.Context, bucket, key string) (storage.ObjectInfo, error) {
	if f.statObjectErr != nil {
		return storage.ObjectInfo{}, f.statObjectErr
	}
	etag := f.defaultETag
	if v, ok := f.etagByKey[bucket+"/"+key]; ok {
		etag = v
	}
	return storage.ObjectInfo{Key: key, Size: f.objectSize, ETag: etag}, nil
}

func (f *fakeStorage) CopyObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string) error {
	if f.copyErr != nil {
		return f.copyErr
	}
	f.copies = append(f.copies, srcBucket+"/"+srcKey+"->"+dstBucket+"/"+dstKey)
	return nil
}

func (f *fakeStorage) DownloadToFile(ctx context.Context, bucket, key, localPath string) error {
	if f.downloadErr != nil {
		return f.downloadErr
	}
	f.downloads = append(f.downloads, bucket+"/"+key)
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(localPath, []byte("content of "+key), 0o644)
}

func (f *fakeStorage) UploadFromFile(ctx context.Context, bucket, key, localPath, contentType string) (int64, error) {
	if f.uploadErr != nil {
		return 0, f.uploadErr
	}
	if _, err := os.Stat(localPath); err != nil {
		return 0, fmt.Errorf("local file missing: %w", err)
	}
	f.uploads = append(f.uploads, bucket+"/"+key)
	f.contentType[key] = contentType
	return f.uploadSize, nil
}

func TestFetchInputs(t *testing.T) {
	store := newFakeStorage()
	scratch := t.TempDir()
	keys := []string{"users/u1/abc_input.docx", "users/u1/def_other.xlsx"}

	localPaths, err := fetchInputs(context.Background(), store, scratch, keys)
	if err != nil {
		t.Fatalf("fetchInputs returned error: %v", err)
	}
	if len(localPaths) != 2 {
		t.Fatalf("expected 2 local paths, got %d", len(localPaths))
	}
	wantFirst := filepath.Join(scratch, "in", "abc_input.docx")
	if localPaths[0] != wantFirst {
		t.Errorf("localPaths[0] = %q, want %q", localPaths[0], wantFirst)
	}
	for _, p := range localPaths {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected downloaded file at %q: %v", p, err)
		}
	}
	if len(store.downloads) != 2 {
		t.Fatalf("expected 2 downloads, got %d", len(store.downloads))
	}
	if store.downloads[0] != "test-uploads/users/u1/abc_input.docx" {
		t.Errorf("download[0] = %q, want uploads-bucket key", store.downloads[0])
	}
}

func TestFetchInputsDownloadError(t *testing.T) {
	store := newFakeStorage()
	store.downloadErr = errors.New("connection refused")

	_, err := fetchInputs(context.Background(), store, t.TempDir(), []string{"users/u1/in.pdf"})
	if err == nil {
		t.Fatal("expected error when download fails")
	}
	if !strings.Contains(err.Error(), "users/u1/in.pdf") {
		t.Errorf("error should mention failing key, got %v", err)
	}
}

func TestFetchInputsEmpty(t *testing.T) {
	store := newFakeStorage()
	localPaths, err := fetchInputs(context.Background(), store, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(localPaths) != 0 {
		t.Errorf("expected no local paths, got %v", localPaths)
	}
}

func TestStoreOutput(t *testing.T) {
	store := newFakeStorage()
	dir := t.TempDir()
	outPath := filepath.Join(dir, "result.pdf")
	if err := os.WriteFile(outPath, []byte("%PDF-1.7"), 0o644); err != nil {
		t.Fatal(err)
	}

	key, size, err := storeOutput(context.Background(), store, "job-123", outPath)
	if err != nil {
		t.Fatalf("storeOutput returned error: %v", err)
	}
	if key != "jobs/job-123/result.pdf" {
		t.Errorf("key = %q, want %q", key, "jobs/job-123/result.pdf")
	}
	if size != store.uploadSize {
		t.Errorf("size = %d, want uploaded size %d", size, store.uploadSize)
	}
	if len(store.uploads) != 1 || store.uploads[0] != "test-outputs/jobs/job-123/result.pdf" {
		t.Errorf("uploads = %v, want outputs-bucket key", store.uploads)
	}
	if ct := store.contentType[key]; ct != "application/pdf" {
		t.Errorf("content type = %q, want application/pdf", ct)
	}
}

func TestStoreOutputUploadError(t *testing.T) {
	store := newFakeStorage()
	store.uploadErr = errors.New("connection reset")

	_, _, err := storeOutput(context.Background(), store, "job-123", "/tmp/out.pdf")
	if err == nil {
		t.Fatal("expected error when upload fails")
	}
}

func TestContentTypeFor(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"out.pdf", "application/pdf"},
		{"OUT.PDF", "application/pdf"},
		{"bundle.zip", "application/zip"},
		{"doc.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{"sheet.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
		{"deck.pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
		{"doc.odt", "application/vnd.oasis.opendocument.text"},
		{"sheet.ods", "application/vnd.oasis.opendocument.spreadsheet"},
		{"deck.odp", "application/vnd.oasis.opendocument.presentation"},
		{"page.html", "text/html"},
		{"page.htm", "text/html"},
		{"notes.txt", "text/plain"},
		{"img.jpg", "image/jpeg"},
		{"img.jpeg", "image/jpeg"},
		{"img.png", "image/png"},
		{"scan.tiff", "image/tiff"},
		{"unknown.bin", "application/octet-stream"},
		{"noextension", "application/octet-stream"},
	}
	for _, tt := range tests {
		if got := contentTypeFor(tt.path); got != tt.want {
			t.Errorf("contentTypeFor(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestMarkRecoverable(t *testing.T) {
	if markRecoverable(nil) != nil {
		t.Error("markRecoverable(nil) should be nil")
	}

	base := errors.New("connection refused")
	wrapped := markRecoverable(base)
	if !isRecoverable(wrapped) {
		t.Error("marked error should be recoverable")
	}
	if !errors.Is(wrapped, base) {
		t.Error("marked error should unwrap to the original error")
	}
	if wrapped.Error() != base.Error() {
		t.Errorf("Error() = %q, want %q", wrapped.Error(), base.Error())
	}
	// A plain conversion error must remain non-recoverable.
	if isRecoverable(base) {
		t.Error("plain error should not be recoverable")
	}
	// Marked errors stay recoverable through further wrapping.
	if !isRecoverable(fmt.Errorf("outer: %w", wrapped)) {
		t.Error("wrapped marked error should remain recoverable")
	}
}

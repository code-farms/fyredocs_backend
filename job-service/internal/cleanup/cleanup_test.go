package cleanup

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"fyredocs/shared/config"
	"fyredocs/shared/storage"

	"job-service/internal/models"
)

// fakeStore records object-storage calls for assertions.
type fakeStore struct {
	removed     []string // "bucket/key"
	aborted     []string // "bucket/key/uploadId"
	incomplete  []storage.IncompleteUpload
	listErr     error
	removeErr   error
	abortErr    error
	listedAge   time.Duration
	listedCalls int
}

func (f *fakeStore) BucketUploads() string { return "uploads" }
func (f *fakeStore) BucketOutputs() string { return "outputs" }

func (f *fakeStore) RemoveObject(_ context.Context, bucket, key string) error {
	f.removed = append(f.removed, bucket+"/"+key)
	return f.removeErr
}

func (f *fakeStore) AbortMultipart(_ context.Context, bucket, key, s3UploadID string) error {
	f.aborted = append(f.aborted, bucket+"/"+key+"/"+s3UploadID)
	return f.abortErr
}

func (f *fakeStore) ListIncompleteUploads(_ context.Context, _ string, olderThan time.Duration) ([]storage.IncompleteUpload, error) {
	f.listedCalls++
	f.listedAge = olderThan
	return f.incomplete, f.listErr
}

func TestBucketFor(t *testing.T) {
	store := &fakeStore{}
	if got := bucketFor(store, "input"); got != "uploads" {
		t.Errorf("bucketFor(input) = %q, want uploads", got)
	}
	if got := bucketFor(store, "output"); got != "outputs" {
		t.Errorf("bucketFor(output) = %q, want outputs", got)
	}
}

func TestRemoveJobObjects(t *testing.T) {
	jobID := uuid.New()

	t.Run("removes one object per metadata row in the right bucket", func(t *testing.T) {
		store := &fakeStore{}
		files := []models.FileMetadata{
			{JobID: jobID, Kind: "input", Path: "uploads/" + jobID.String() + "/doc.pdf"},
			{JobID: jobID, Kind: "output", Path: "jobs/" + jobID.String() + "/converted.docx"},
		}
		removeJobObjects(context.Background(), store, files)

		want := []string{
			"uploads/uploads/" + jobID.String() + "/doc.pdf",
			"outputs/jobs/" + jobID.String() + "/converted.docx",
		}
		if len(store.removed) != len(want) {
			t.Fatalf("removed %d objects, want %d: %v", len(store.removed), len(want), store.removed)
		}
		for i, w := range want {
			if store.removed[i] != w {
				t.Errorf("removed[%d] = %q, want %q", i, store.removed[i], w)
			}
		}
	})

	t.Run("skips legacy filesystem paths", func(t *testing.T) {
		store := &fakeStore{}
		files := []models.FileMetadata{
			{JobID: jobID, Kind: "input", Path: "/app/uploads/" + jobID.String() + "/doc.pdf"},
			{JobID: jobID, Kind: "output", Path: "/app/outputs/converted_" + jobID.String() + "_123.docx"},
			{JobID: jobID, Kind: "output", Path: "jobs/" + jobID.String() + "/kept.docx"},
		}
		removeJobObjects(context.Background(), store, files)

		if len(store.removed) != 1 {
			t.Fatalf("removed %d objects, want 1 (legacy paths skipped): %v", len(store.removed), store.removed)
		}
		if store.removed[0] != "outputs/jobs/"+jobID.String()+"/kept.docx" {
			t.Errorf("removed wrong object: %q", store.removed[0])
		}
	})

	t.Run("no rows is a no-op", func(t *testing.T) {
		store := &fakeStore{}
		removeJobObjects(context.Background(), store, nil)
		if len(store.removed) != 0 {
			t.Errorf("expected no removals, got %v", store.removed)
		}
	})
}

func TestReapExpiredUploadObjects(t *testing.T) {
	notConsumed := func(string) bool { return false }
	consumed := func(string) bool { return true }

	t.Run("aborts multipart and removes unconsumed object", func(t *testing.T) {
		store := &fakeStore{}
		reapExpiredUploadObjects(context.Background(), store, "uploads/u1/file.pdf", "s3-upload-1", notConsumed)

		if len(store.aborted) != 1 || store.aborted[0] != "uploads/uploads/u1/file.pdf/s3-upload-1" {
			t.Errorf("aborted = %v, want one abort for s3-upload-1", store.aborted)
		}
		if len(store.removed) != 1 || store.removed[0] != "uploads/uploads/u1/file.pdf" {
			t.Errorf("removed = %v, want the upload object removed", store.removed)
		}
	})

	t.Run("no multipart id skips abort", func(t *testing.T) {
		store := &fakeStore{}
		reapExpiredUploadObjects(context.Background(), store, "uploads/u2/file.pdf", "", notConsumed)

		if len(store.aborted) != 0 {
			t.Errorf("expected no aborts, got %v", store.aborted)
		}
		if len(store.removed) != 1 {
			t.Errorf("expected object removal, got %v", store.removed)
		}
	})

	t.Run("consumed object is kept", func(t *testing.T) {
		store := &fakeStore{}
		reapExpiredUploadObjects(context.Background(), store, "uploads/u3/file.pdf", "s3-upload-3", consumed)

		if len(store.aborted) != 1 {
			t.Errorf("expected abort even when consumed, got %v", store.aborted)
		}
		if len(store.removed) != 0 {
			t.Errorf("consumed object must not be removed, got %v", store.removed)
		}
	})

	t.Run("empty key is a no-op", func(t *testing.T) {
		store := &fakeStore{}
		reapExpiredUploadObjects(context.Background(), store, "", "s3-upload-4", notConsumed)

		if len(store.aborted) != 0 || len(store.removed) != 0 {
			t.Errorf("expected no calls for empty key, got aborted=%v removed=%v", store.aborted, store.removed)
		}
	})
}

func TestAbortStaleMultipartUploads(t *testing.T) {
	t.Run("aborts each stale upload", func(t *testing.T) {
		store := &fakeStore{
			incomplete: []storage.IncompleteUpload{
				{Key: "uploads/a/file1.pdf", UploadID: "id-1", Initiated: time.Now().Add(-48 * time.Hour)},
				{Key: "uploads/b/file2.pdf", UploadID: "id-2", Initiated: time.Now().Add(-30 * time.Hour)},
			},
		}
		abortStaleMultipartUploads(context.Background(), store)

		if store.listedCalls != 1 {
			t.Fatalf("ListIncompleteUploads called %d times, want 1", store.listedCalls)
		}
		if store.listedAge != config.StaleMultipartAge() {
			t.Errorf("listed olderThan = %v, want %v", store.listedAge, config.StaleMultipartAge())
		}
		want := []string{
			"uploads/uploads/a/file1.pdf/id-1",
			"uploads/uploads/b/file2.pdf/id-2",
		}
		if len(store.aborted) != len(want) {
			t.Fatalf("aborted %d uploads, want %d: %v", len(store.aborted), len(want), store.aborted)
		}
		for i, w := range want {
			if store.aborted[i] != w {
				t.Errorf("aborted[%d] = %q, want %q", i, store.aborted[i], w)
			}
		}
	})

	t.Run("nothing stale is a no-op", func(t *testing.T) {
		store := &fakeStore{}
		abortStaleMultipartUploads(context.Background(), store)
		if len(store.aborted) != 0 {
			t.Errorf("expected no aborts, got %v", store.aborted)
		}
	})
}

func TestUploadReapAge(t *testing.T) {
	t.Run("is twice the session TTL", func(t *testing.T) {
		t.Setenv("UPLOAD_TTL", "")
		if got := uploadReapAge(); got != 2*30*time.Minute {
			t.Errorf("uploadReapAge = %v, want 1h (2 × 30m default)", got)
		}
	})

	t.Run("tracks UPLOAD_TTL overrides", func(t *testing.T) {
		t.Setenv("UPLOAD_TTL", "45m")
		if got := uploadReapAge(); got != 90*time.Minute {
			t.Errorf("uploadReapAge = %v, want 90m", got)
		}
	})
}

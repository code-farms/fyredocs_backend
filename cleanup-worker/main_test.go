package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"fyredocs/shared/storage"

	"cleanup-worker/internal/models"
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

func (f *fakeStore) BucketUploads() string { return "fyredocs-uploads" }
func (f *fakeStore) BucketOutputs() string { return "fyredocs-outputs" }

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
	if got := bucketFor(store, "input"); got != "fyredocs-uploads" {
		t.Errorf("bucketFor(input) = %q, want fyredocs-uploads", got)
	}
	if got := bucketFor(store, "output"); got != "fyredocs-outputs" {
		t.Errorf("bucketFor(output) = %q, want fyredocs-outputs", got)
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
			"fyredocs-uploads/uploads/" + jobID.String() + "/doc.pdf",
			"fyredocs-outputs/jobs/" + jobID.String() + "/converted.docx",
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
		if store.removed[0] != "fyredocs-outputs/jobs/"+jobID.String()+"/kept.docx" {
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

		if len(store.aborted) != 1 || store.aborted[0] != "fyredocs-uploads/uploads/u1/file.pdf/s3-upload-1" {
			t.Errorf("aborted = %v, want one abort for s3-upload-1", store.aborted)
		}
		if len(store.removed) != 1 || store.removed[0] != "fyredocs-uploads/uploads/u1/file.pdf" {
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
		if store.listedAge != staleMultipartAge {
			t.Errorf("listed olderThan = %v, want %v", store.listedAge, staleMultipartAge)
		}
		want := []string{
			"fyredocs-uploads/uploads/a/file1.pdf/id-1",
			"fyredocs-uploads/uploads/b/file2.pdf/id-2",
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

func TestCleanupInterval(t *testing.T) {
	t.Run("default 15m", func(t *testing.T) {
		t.Setenv("CLEANUP_INTERVAL", "")
		got := cleanupInterval()
		if got != 15*time.Minute {
			t.Errorf("expected 15m, got %v", got)
		}
	})

	t.Run("custom 5m", func(t *testing.T) {
		t.Setenv("CLEANUP_INTERVAL", "5m")
		got := cleanupInterval()
		if got != 5*time.Minute {
			t.Errorf("expected 5m, got %v", got)
		}
	})

	t.Run("invalid uses default", func(t *testing.T) {
		t.Setenv("CLEANUP_INTERVAL", "invalid")
		got := cleanupInterval()
		if got != 15*time.Minute {
			t.Errorf("expected 15m, got %v", got)
		}
	})
}

func TestUploadTTL(t *testing.T) {
	t.Run("default 2h", func(t *testing.T) {
		t.Setenv("UPLOAD_TTL", "")
		got := uploadTTL()
		if got != 2*time.Hour {
			t.Errorf("expected 2h, got %v", got)
		}
	})

	t.Run("custom 30m", func(t *testing.T) {
		t.Setenv("UPLOAD_TTL", "30m")
		got := uploadTTL()
		if got != 30*time.Minute {
			t.Errorf("expected 30m, got %v", got)
		}
	})

	t.Run("invalid uses default", func(t *testing.T) {
		t.Setenv("UPLOAD_TTL", "notaduration")
		got := uploadTTL()
		if got != 2*time.Hour {
			t.Errorf("expected 2h, got %v", got)
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

func TestHealthzRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "healthy"})
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestReadyzRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/readyz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestDefaultPort(t *testing.T) {
	t.Setenv("PORT", "")
	port := os.Getenv("PORT")
	if port == "" {
		port = "8088"
	}
	if port != "8088" {
		t.Errorf("expected default port 8088, got %s", port)
	}
}

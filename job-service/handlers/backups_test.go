// Tests for the CreateBackup handler. The invariants under guard: backups are
// account-only (guests and anonymous callers are rejected before any storage
// work), the storage key is minted server-side under backups/{userId}/ so a
// client can never choose another user's prefix, client file names are reduced
// to a safe base name, and the whole flow is Redis+object-store only — no
// database row is written here, so the happy path is fully testable offline.
package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// newBackupRouter mounts CreateBackup, optionally behind an auth middleware
// that injects uid as the verified caller (the gateway's job in production).
func newBackupRouter(uid *uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if uid != nil {
		r.Use(ownerAuth(*uid))
	}
	r.POST("/api/backups", CreateBackup)
	return r
}

// pdfContents sniffs as application/pdf, which the backup pseudo-tool allows.
var pdfContents = []byte("%PDF-1.7\nbackup test contents")

func TestCreateBackup_NilStoreIs503Shape(t *testing.T) {
	// A nil objStore means the service booted without storage; the handler
	// must answer 500 (misconfigured) instead of panicking — checked before
	// auth so the guard holds for every caller.
	prev := objStore
	objStore = nil
	t.Cleanup(func() { objStore = prev })

	uid := uuid.New()
	r := newBackupRouter(&uid)
	rec, env := doJSON(t, r, http.MethodPost, "/api/backups", createBackupRequest{UploadID: "u1"}, nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for nil object store", rec.Code)
	}
	if env.Error == nil || env.Error.Code != "SERVER_ERROR" {
		t.Errorf("error = %+v, want code SERVER_ERROR", env.Error)
	}
}

func TestCreateBackup_RequiresAccount(t *testing.T) {
	_, client := withMiniRedis(t)
	fs := withFakeStore(t)
	uploadID := "upl-backup-auth"
	seedUploadObject(t, client, fs, uploadID, "report.pdf", pdfContents)

	t.Run("anonymous rejected", func(t *testing.T) {
		r := newBackupRouter(nil)
		rec, env := doJSON(t, r, http.MethodPost, "/api/backups", createBackupRequest{UploadID: uploadID}, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
		}
		if env.Error == nil || env.Error.Code != "AUTH_UNAUTHORIZED" {
			t.Errorf("error = %+v, want code AUTH_UNAUTHORIZED", env.Error)
		}
	})

	t.Run("guest token rejected", func(t *testing.T) {
		// Backups are account-backed by definition: a guest_token cookie is a
		// valid identity for jobs, but it must never satisfy the backup gate.
		r := newBackupRouter(nil)
		rec, _ := doJSON(t, r, http.MethodPost, "/api/backups",
			createBackupRequest{UploadID: uploadID},
			map[string]string{"Cookie": "guest_token=guest-tok-backup"})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 for a guest caller", rec.Code)
		}
	})

	// Rejection happens before any storage or session mutation.
	for bucketKey := range fs.objects {
		if strings.HasPrefix(bucketKey, fs.BucketOutputs()+"/") {
			t.Errorf("unauthenticated request created output object %q", bucketKey)
		}
	}
	exists, err := client.Exists(context.Background(), "upload:"+uploadID).Result()
	if err != nil {
		t.Fatal(err)
	}
	if exists != 1 {
		t.Error("upload session must be preserved when the caller is rejected")
	}
}

func TestCreateBackup_InvalidBody400(t *testing.T) {
	withMiniRedis(t)
	withFakeStore(t)
	uid := uuid.New()
	r := newBackupRouter(&uid)

	tests := []struct {
		name string
		body string
	}{
		{"empty body", ""},
		{"truncated json", `{"uploadId":`},
		{"wrong field type", `{"uploadId":123}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/backups", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
			}
		})
	}

	t.Run("empty uploadId", func(t *testing.T) {
		// Valid JSON, but no session to consume — same client-visible 400.
		rec, _ := doJSON(t, r, http.MethodPost, "/api/backups", createBackupRequest{UploadID: ""}, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400 for empty uploadId", rec.Code)
		}
	})
}

func TestCreateBackup_UnknownUpload400(t *testing.T) {
	withMiniRedis(t)
	withFakeStore(t)
	uid := uuid.New()
	r := newBackupRouter(&uid)

	rec, env := doJSON(t, r, http.MethodPost, "/api/backups", createBackupRequest{UploadID: "never-existed"}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if env.Error == nil || env.Error.Code != "INVALID_INPUT" {
		t.Errorf("error = %+v, want code INVALID_INPUT", env.Error)
	}
}

func TestCreateBackup_RejectsNonBackupableContent(t *testing.T) {
	_, client := withMiniRedis(t)
	fs := withFakeStore(t)
	uid := uuid.New()
	r := newBackupRouter(&uid)

	// ELF magic sniffs as application/octet-stream — outside the share/backup
	// content union, so the copy must never happen.
	uploadID := "upl-backup-elf"
	seedUploadObject(t, client, fs, uploadID, "innocent.pdf", []byte{0x7F, 'E', 'L', 'F', 2, 1, 1, 0, 0, 0, 0, 0})

	rec, _ := doJSON(t, r, http.MethodPost, "/api/backups", createBackupRequest{UploadID: uploadID}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for executable content (body %s)", rec.Code, rec.Body.String())
	}
	for bucketKey := range fs.objects {
		if strings.HasPrefix(bucketKey, fs.BucketOutputs()+"/") {
			t.Errorf("rejected upload was still copied to %q", bucketKey)
		}
	}
}

func TestCreateBackup_HappyPath(t *testing.T) {
	_, client := withMiniRedis(t)
	fs := withFakeStore(t)
	uid := uuid.New()
	r := newBackupRouter(&uid)

	uploadID := "upl-backup-ok"
	seedUploadObject(t, client, fs, uploadID, "report.pdf", pdfContents)

	rec, env := doJSON(t, r, http.MethodPost, "/api/backups", createBackupRequest{UploadID: uploadID}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}

	storageKey, _ := env.Data["storageKey"].(string)
	// The key is minted server-side: backups/{userId}/{uuid}/{name}. The
	// userId segment is what document-service validates ownership against,
	// so it must be the authenticated caller's ID — never client input.
	wantPrefix := "backups/" + uid.String() + "/"
	if !strings.HasPrefix(storageKey, wantPrefix) {
		t.Errorf("storageKey = %q, want prefix %q", storageKey, wantPrefix)
	}
	if !strings.HasSuffix(storageKey, "/report.pdf") {
		t.Errorf("storageKey = %q, want suffix /report.pdf", storageKey)
	}
	segments := strings.Split(storageKey, "/")
	if len(segments) != 4 {
		t.Fatalf("storageKey = %q, want 4 segments backups/{userId}/{uuid}/{name}", storageKey)
	}
	if _, err := uuid.Parse(segments[2]); err != nil {
		t.Errorf("collision segment %q is not a UUID: %v", segments[2], err)
	}

	if !fs.hasObject(fs.BucketOutputs(), storageKey) {
		t.Error("backup object missing from the outputs bucket")
	}
	if !fs.hasObject(fs.BucketUploads(), "uploads/"+uploadID+"/report.pdf") {
		t.Error("source object must be copied, not moved (sweeps reap it later)")
	}

	if got, _ := env.Data["fileName"].(string); got != "report.pdf" {
		t.Errorf("fileName = %q, want report.pdf", got)
	}
	if got, _ := env.Data["contentType"].(string); got != "application/pdf" {
		t.Errorf("contentType = %q, want application/pdf", got)
	}
	if got, _ := env.Data["fileSize"].(float64); int64(got) != int64(len(pdfContents)) {
		t.Errorf("fileSize = %v, want %d (true size from StatObject)", got, len(pdfContents))
	}

	// The upload slot is freed after the copy (same contract as jobs/shares).
	exists, err := client.Exists(context.Background(), "upload:"+uploadID).Result()
	if err != nil {
		t.Fatal(err)
	}
	if exists != 0 {
		t.Error("upload session must be released after a successful backup")
	}
}

func TestCreateBackup_SanitizesClientFileName(t *testing.T) {
	tests := []struct {
		name     string
		fileName string
		wantBase string
	}{
		{"path traversal reduced to base", "../../../etc/passwd.pdf", "passwd.pdf"},
		{"nested path reduced to base", "nested/dir/evil.pdf", "evil.pdf"},
		{"dot-only falls back to original name", "..", "report.pdf"},
		{"blank falls back to original name", "   ", "report.pdf"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, client := withMiniRedis(t)
			fs := withFakeStore(t)
			uid := uuid.New()
			r := newBackupRouter(&uid)

			uploadID := "upl-backup-name"
			seedUploadObject(t, client, fs, uploadID, "report.pdf", pdfContents)

			rec, env := doJSON(t, r, http.MethodPost, "/api/backups",
				createBackupRequest{UploadID: uploadID, FileName: tt.fileName}, nil)
			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
			}
			storageKey, _ := env.Data["storageKey"].(string)
			if strings.Contains(storageKey, "..") {
				t.Errorf("storageKey %q contains a dot-segment", storageKey)
			}
			if !strings.HasSuffix(storageKey, "/"+tt.wantBase) {
				t.Errorf("storageKey = %q, want suffix /%s", storageKey, tt.wantBase)
			}
			if !strings.HasPrefix(storageKey, "backups/"+uid.String()+"/") {
				t.Errorf("storageKey = %q escaped the caller's backups/ prefix", storageKey)
			}
		})
	}
}

// TestCreateBackup_ForeignUploadSessionDenied is the regression guard for the
// consumption-side half of finding F4 (docs/developer/architecture/
// backend-hardening.md §3.20): the multipart endpoints always enforced the
// session's "owner" field, but consumeUpload once did not — any caller who
// learned a live uploadId could copy another user's bytes into their own
// backups/ prefix (likewise jobs and shares). The response must be the same
// "upload not found" shape as a missing session, so the endpoint cannot be
// used to probe which uploadIds exist.
func TestCreateBackup_ForeignUploadSessionDenied(t *testing.T) {
	_, client := withMiniRedis(t)
	fs := withFakeStore(t)
	caller := uuid.New()
	r := newBackupRouter(&caller)

	uploadID := "upl-backup-foreign"
	seedUploadObject(t, client, fs, uploadID, "report.pdf", pdfContents)
	// Bind the session to a different account, as InitUpload would have.
	if err := client.HSet(context.Background(), "upload:"+uploadID, "owner", "user:"+uuid.NewString()).Err(); err != nil {
		t.Fatal(err)
	}

	rec, _ := doJSON(t, r, http.MethodPost, "/api/backups", createBackupRequest{UploadID: uploadID}, nil)
	if rec.Code == http.StatusCreated {
		t.Fatalf("foreign upload session was consumed — cross-user data access (body %s)", rec.Body.String())
	}
	fs.mu.Lock()
	for key := range fs.objects {
		if strings.Contains(key, "backups/"+caller.String()+"/") {
			t.Errorf("foreign bytes were copied into the caller's prefix despite the denial: %s", key)
		}
	}
	fs.mu.Unlock()

	t.Run("owner-bound session still consumable by its owner", func(t *testing.T) {
		ownedID := "upl-backup-owned"
		seedUploadObject(t, client, fs, ownedID, "mine.pdf", pdfContents)
		if err := client.HSet(context.Background(), "upload:"+ownedID, "owner", "user:"+caller.String()).Err(); err != nil {
			t.Fatal(err)
		}
		rec, env := doJSON(t, r, http.MethodPost, "/api/backups", createBackupRequest{UploadID: ownedID}, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("owner denied their own session: %d (body %s)", rec.Code, rec.Body.String())
		}
		storageKey, _ := env.Data["storageKey"].(string)
		if !strings.HasPrefix(storageKey, "backups/"+caller.String()+"/") {
			t.Errorf("storageKey = %q, want the caller's prefix", storageKey)
		}
	})
}

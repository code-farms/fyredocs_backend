// Document backup (Phase 9): turns a completed upload into a durable object
// under backups/{userId}/… — the byte half of "back up to cloud".
//
// Flow mirrors share links (shares.go): the client uploads through the normal
// presigned protocol, then trades the uploadId here. The object is
// server-side-copied OUT of the upload/job lifecycle into the outputs bucket
// under a caller-derived prefix, and the returned storage key is what the
// client registers with document-service (POST /api/documents {storagePath}).
// document-service enforces that a registered key sits under the caller's own
// backups/ prefix and deletes the object when the document is purged —
// job-service's sweeps deliberately never touch backups/.
//
// Auth is REQUIRED: backups are account-backed by definition (guest bytes
// have nowhere to be restored to).
package handlers

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"fyredocs/shared/response"
)

type createBackupRequest struct {
	UploadID string `json:"uploadId"`
	FileName string `json:"fileName"`
}

type createBackupResponse struct {
	StorageKey  string `json:"storageKey"`
	FileName    string `json:"fileName"`
	FileSize    int64  `json:"fileSize"`
	ContentType string `json:"contentType"`
}

// CreateBackup copies a completed upload into the caller's backups/ prefix
// and returns the storage key for document registration.
func CreateBackup(c *gin.Context) {
	if objStore == nil {
		response.InternalError(c, response.CodeServerError, "Backup is unavailable. Please try again later.")
		return
	}
	userID := authUserID(c)
	if userID == nil {
		response.Unauthorized(c, response.CodeUnauthorized, "Please log in to back up documents.")
		return
	}

	var req createBackupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, response.CodeInvalidInput, "Invalid request body.")
		return
	}

	// The "backup" pseudo-tool applies the shareable-type gates (extension +
	// MIME sniff) inside the normal upload consumption path.
	consumed, err := consumeUpload(c.Request.Context(), "backup", strings.TrimSpace(req.UploadID), uploadOwnerID(c, false))
	if err != nil {
		response.BadRequest(c, response.CodeInvalidInput, err.Error())
		return
	}

	fileName := sanitizeFileName(req.FileName)
	if fileName == "" {
		fileName = sanitizeFileName(consumed.OriginalName)
	}
	if fileName == "" {
		response.BadRequest(c, response.CodeInvalidInput, "A file name is required.")
		return
	}

	// backups/{userId}/{uuid}/{name}: the userId segment is what
	// document-service validates ownership against; the uuid segment keeps
	// same-named backups from colliding.
	storageKey := fmt.Sprintf("backups/%s/%s/%s", userID, uuid.Must(uuid.NewV7()), fileName)

	if err := objStore.CopyObject(c.Request.Context(), objStore.BucketUploads(), consumed.Key,
		objStore.BucketOutputs(), storageKey); err != nil {
		response.InternalErrorf(c, response.CodeServerError, "Could not store the backup. Please try again.", err,
			"op", "backup.copy_object", "uploadId", req.UploadID)
		return
	}

	// The upload served its purpose; free the slot (jobs and shares do the same).
	releaseUpload(c.Request.Context(), req.UploadID)

	response.Created(c, "Backup stored", createBackupResponse{
		StorageKey:  storageKey,
		FileName:    fileName,
		FileSize:    consumed.Size,
		ContentType: contentTypeForBackup(fileName),
	})
}

// contentTypeForBackup derives a content type from the extension — the same
// ext-based mapping shares use (mime.TypeByExtension), kept locally simple.
func contentTypeForBackup(fileName string) string {
	if idx := strings.LastIndex(fileName, "."); idx >= 0 {
		switch strings.ToLower(fileName[idx:]) {
		case ".pdf":
			return "application/pdf"
		case ".png":
			return "image/png"
		case ".jpg", ".jpeg":
			return "image/jpeg"
		case ".webp":
			return "image/webp"
		case ".txt":
			return "text/plain"
		case ".zip":
			return "application/zip"
		case ".docx":
			return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
		}
	}
	return "application/octet-stream"
}

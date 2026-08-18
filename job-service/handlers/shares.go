// Share links (Phase 6): expiring public download links for user files.
//
// Flow: the client uploads bytes through the normal presigned-upload protocol,
// then POSTs the uploadId here. The object is server-side-copied into the
// outputs bucket under shares/{id}/... — detaching it from the upload (and any
// job-TTL) lifecycle — and a 256-bit redeem token is minted. Only the token's
// SHA-256 is stored; the public redeem endpoint (GET /share/:token) 302s to a
// short-lived presigned URL with attachment disposition, mirroring job
// downloads. The cleanup sweep owns expired/revoked shares' objects.
package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"fyredocs/shared/logger"
	"fyredocs/shared/response"

	"job-service/internal/models"
)

// shareFileExts is the extension allowlist for shareable files — the document
// types the platform's library holds. Deliberately NOT fail-open: a share URL
// is public distribution on our domain, so executables and unknown types are
// refused even though the redeem forces attachment disposition.
var shareFileExts = map[string]bool{
	".pdf": true, ".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".tif": true, ".tiff": true,
	".txt": true, ".zip": true, ".html": true,
	".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".ppt": true, ".pptx": true,
	".odt": true, ".ods": true, ".odp": true,
}

// shareExpiries is the client-selectable lifetime allowlist.
var shareExpiries = map[string]time.Duration{
	"1h":  time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
}

const defaultShareExpiry = "24h"

// shareDownloadURLExpiry matches the job-download presign window: long enough
// for a redirect follow, short enough that a leaked presign is useless.
const shareDownloadURLExpiry = 5 * time.Minute

// shareTokenBytes sizes the redeem token: 32 random bytes = 256 bits, far
// beyond enumeration even on an unauthenticated endpoint.
const shareTokenBytes = 32

type createShareRequest struct {
	UploadID  string `json:"uploadId"`
	ExpiresIn string `json:"expiresIn"`
}

// createShareResponse is the ONE response that carries the raw token (inside
// the URL); it is never recoverable afterwards.
type createShareResponse struct {
	models.Share
	URL string `json:"url"`
}

// hashShareToken hex-encodes the SHA-256 of a redeem token — the only form
// that is stored or queried.
func hashShareToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// newShareToken mints a URL-safe 256-bit token.
func newShareToken() (string, error) {
	buf := make([]byte, shareTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// shareBaseURL resolves the public origin for share URLs. SHARE_BASE_URL is
// authoritative (the gateway's public origin); the request Host is only a
// dev fallback and assumes the gateway forwards it.
func shareBaseURL(c *gin.Context) string {
	if base := strings.TrimRight(os.Getenv("SHARE_BASE_URL"), "/"); base != "" {
		return base
	}
	scheme := "https"
	if c.Request.TLS == nil && !strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https") {
		scheme = "http"
	}
	return fmt.Sprintf("%s://%s", scheme, c.Request.Host)
}

// CreateShare turns a completed upload into an expiring public link.
// Guests may share (UserID stays null — their shares expire, never revoke).
func CreateShare(c *gin.Context) {
	if objStore == nil || models.DB == nil {
		response.InternalError(c, response.CodeServerError, "Sharing is unavailable. Please try again later.")
		return
	}

	var req createShareRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, response.CodeInvalidInput, "Invalid request body.")
		return
	}
	expiresIn := req.ExpiresIn
	if expiresIn == "" {
		expiresIn = defaultShareExpiry
	}
	lifetime, ok := shareExpiries[expiresIn]
	if !ok {
		response.BadRequest(c, response.CodeInvalidInput, "expiresIn must be one of: 1h, 24h, 7d.")
		return
	}

	// The "share" pseudo-tool applies the shareable-type gates (extension +
	// MIME sniff) inside the normal upload consumption path.
	consumed, err := consumeUpload(c.Request.Context(), "share", strings.TrimSpace(req.UploadID), uploadOwnerID(c, false))
	if err != nil {
		response.BadRequest(c, response.CodeInvalidInput, err.Error())
		return
	}

	token, err := newShareToken()
	if err != nil {
		response.InternalErrorf(c, response.CodeServerError, "Could not create the share link. Please try again.", err,
			"op", "share.token")
		return
	}

	id := uuid.Must(uuid.NewV7())
	fileName := sanitizeFileName(consumed.OriginalName)
	objectKey := fmt.Sprintf("shares/%s/%s", id, fileName)

	if err := objStore.CopyObject(c.Request.Context(), objStore.BucketUploads(), consumed.Key,
		objStore.BucketOutputs(), objectKey); err != nil {
		response.InternalErrorf(c, response.CodeServerError, "Could not create the share link. Please try again.", err,
			"op", "share.copy_object", "uploadId", req.UploadID)
		return
	}

	var contentType *string
	if ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(fileName))); ct != "" {
		contentType = &ct
	}

	share := models.Share{
		ID:          id,
		TokenHash:   hashShareToken(token),
		ObjectKey:   objectKey,
		FileName:    fileName,
		ContentType: contentType,
		FileSize:    consumed.Size,
		UserID:      authUserID(c),
		ExpiresAt:   time.Now().UTC().Add(lifetime),
	}
	if err := models.DB.Create(&share).Error; err != nil {
		// Roll the object back — nothing references it after this failure.
		if rmErr := objStore.RemoveObject(c.Request.Context(), objStore.BucketOutputs(), objectKey); rmErr != nil {
			logger.LogWarn(c.Request.Context(), "share.rollback_object", rmErr, "key", objectKey)
		}
		response.InternalErrorf(c, response.CodeServerError, "Could not create the share link. Please try again.", err,
			"op", "db.shares.create")
		return
	}

	// The upload served its purpose; free the slot (jobs do the same).
	releaseUpload(c.Request.Context(), req.UploadID)

	response.Created(c, "Share link created", createShareResponse{
		Share: share,
		URL:   fmt.Sprintf("%s/share/%s", shareBaseURL(c), token),
	})
}

// RedeemShare is the PUBLIC endpoint behind a share URL: no auth, its own
// rate limit, token looked up by hash only. Responds 302 to a presigned
// download or 404 — expired, revoked, and unknown are indistinguishable on
// purpose.
func RedeemShare(c *gin.Context) {
	if objStore == nil || models.DB == nil {
		response.InternalError(c, response.CodeServerError, "Sharing is unavailable. Please try again later.")
		return
	}
	token := c.Param("token")
	// Cheap shape check before hashing: base64url of 32 bytes is 43 chars.
	if len(token) < 40 || len(token) > 64 {
		response.NotFound(c, response.CodeNotFound, "This share link is invalid or has expired.")
		return
	}

	var share models.Share
	err := models.DB.Where("token_hash = ?", hashShareToken(token)).First(&share).Error
	if err != nil || share.RevokedAt != nil || time.Now().UTC().After(share.ExpiresAt) {
		if err != nil && err != gorm.ErrRecordNotFound {
			logger.LogErr(c.Request.Context(), "db.shares.lookup", err)
		}
		response.NotFound(c, response.CodeNotFound, "This share link is invalid or has expired.")
		return
	}

	// Best-effort counter; a lost increment is not worth failing a download.
	if err := models.DB.Model(&models.Share{}).Where("id = ?", share.ID).
		UpdateColumn("download_count", gorm.Expr("download_count + 1")).Error; err != nil {
		logger.LogWarn(c.Request.Context(), "db.shares.count", err, "shareId", share.ID)
	}

	params := url.Values{}
	params.Set("response-content-disposition", fmt.Sprintf(`attachment; filename="%s"`, share.FileName))
	if share.ContentType != nil {
		params.Set("response-content-type", *share.ContentType)
	}
	presigned, err := objStore.PresignGet(c.Request.Context(), objStore.BucketOutputs(), share.ObjectKey,
		shareDownloadURLExpiry, params)
	if err != nil {
		response.InternalErrorf(c, response.CodeServerError, "Could not prepare the download. Please try again.", err,
			"op", "share.presign", "shareId", share.ID)
		return
	}
	c.Redirect(302, presigned)
}

// RevokeShare kills a link early. Owner-only: guest shares carry no user and
// simply expire.
func RevokeShare(c *gin.Context) {
	userID := authUserID(c)
	if userID == nil {
		response.Unauthorized(c, response.CodeUnauthorized, "Please log in to revoke share links.")
		return
	}
	shareID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, response.CodeInvalidInput, "Invalid share id.")
		return
	}

	// Ownership folded into the WHERE: someone else's share 404s, never 403s
	// (no existence oracle).
	res := models.DB.Model(&models.Share{}).
		Where("id = ? AND user_id = ? AND revoked_at IS NULL", shareID, userID).
		UpdateColumn("revoked_at", time.Now().UTC())
	if res.Error != nil {
		response.InternalErrorf(c, response.CodeServerError, "Could not revoke the share link. Please try again.", res.Error,
			"op", "db.shares.revoke", "shareId", shareID)
		return
	}
	if res.RowsAffected == 0 {
		response.NotFound(c, response.CodeNotFound, "Share link not found.")
		return
	}
	response.OK(c, "Share link revoked", gin.H{"id": shareID})
}

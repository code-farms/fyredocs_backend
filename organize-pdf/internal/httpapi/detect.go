// Package httpapi exposes organize-pdf's internal (service-to-service) HTTP
// endpoints. These routes are only reachable on the Docker network — the
// public gateway never proxies to this service directly.
package httpapi

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"fyredocs/shared/authverify"
	"fyredocs/shared/logger"
	"fyredocs/shared/response"
	"fyredocs/shared/storage"

	"organize-pdf/internal/imaging"
)

// maxDetectObjectBytes caps the object size fetched for edge detection.
const maxDetectObjectBytes = 30 << 20 // 30 MiB

// ObjectGetter is the storage surface detect-edges needs; satisfied by
// *storage.Client and fakeable in tests.
type ObjectGetter interface {
	GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, error)
	StatObject(ctx context.Context, bucket, key string) (storage.ObjectInfo, error)
	BucketUploads() string
}

// detectRequest carries one image for synchronous edge detection, relayed from
// job-service on behalf of the mobile scanner.
type detectRequest struct {
	Bucket string `json:"bucket" binding:"omitempty,max=63"`
	Key    string `json:"key" binding:"required,max=1024"`
}

// detectResponse returns the detected document quad in normalized 0..1
// coordinates, so the client can scale it to whatever preview size it is showing.
type detectResponse struct {
	Corners    imaging.Quad `json:"corners"`
	Confidence float64      `json:"confidence"`
	Width      int          `json:"width"`
	Height     int          `json:"height"`
}

// RegisterInternalRoutes mounts the /internal/v1 group behind the shared
// INTERNAL_API_TOKEN middleware. Requests must carry the secret in
// X-Internal-Token; an empty token fails closed (503) rather than skipping the
// check, so a missing env var can never silently reopen the route.
func RegisterInternalRoutes(r *gin.Engine, store ObjectGetter, internalToken string) {
	internal := r.Group("/internal/v1", authverify.InternalTokenGin(internalToken))
	internal.POST("/detect-edges", detectEdgesHandler(store))
}

// detectEdgesHandler runs edge detection on a posted frame.
//
// Synchronous and deliberately cheap: the scanner calls it repeatedly at camera
// frame rate, so it must never queue work or perform I/O beyond decoding. A
// failure to find a document returns a full-image quad rather than an error, since
// "no document detected" is the normal case while the user is still aiming.
func detectEdgesHandler(store ObjectGetter) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req detectRequest
		if !response.BindJSON(c, &req) {
			return
		}
		if strings.TrimSpace(req.Key) == "" {
			response.BadRequest(c, response.CodeInvalidInput, "A storage key is required.")
			return
		}

		bucket := strings.TrimSpace(req.Bucket)
		if bucket == "" {
			bucket = store.BucketUploads()
		}
		key := strings.TrimSpace(req.Key)

		info, err := store.StatObject(c.Request.Context(), bucket, key)
		if err != nil {
			// Log so a storage outage isn't silently masked as a 404.
			logger.LogWarn(c.Request.Context(), "s3.stat_detect_object", err, "bucket", bucket, "key", key)
			response.NotFound(c, "OBJECT_NOT_FOUND", "The referenced upload could not be found.")
			return
		}
		if info.Size > maxDetectObjectBytes {
			response.Err(c, http.StatusRequestEntityTooLarge, "IMAGE_TOO_LARGE", "The image is too large for edge detection.")
			return
		}

		obj, err := store.GetObject(c.Request.Context(), bucket, key)
		if err != nil {
			response.InternalErrorf(c, "STORAGE_ERROR", "Failed to read the uploaded image.", err,
				"op", "s3.get_detect_object", "bucket", bucket, "key", key)
			return
		}
		defer obj.Close()

		img, _, err := imaging.DecodeReader(io.LimitReader(obj, maxDetectObjectBytes+1))
		if err != nil {
			response.Err(c, http.StatusUnprocessableEntity, "UNDECODABLE_IMAGE", "The upload is not a decodable image.")
			return
		}

		quad, confidence := imaging.DetectDocumentQuad(img)
		b := img.Bounds()
		response.OK(c, "Edges detected", detectResponse{
			Corners:    quad,
			Confidence: confidence,
			Width:      b.Dx(),
			Height:     b.Dy(),
		})
	}
}

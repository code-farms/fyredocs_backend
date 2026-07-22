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

type detectRequest struct {
	Bucket string `json:"bucket"`
	Key    string `json:"key"`
}

type detectResponse struct {
	Corners    imaging.Quad `json:"corners"`
	Confidence float64      `json:"confidence"`
	Width      int          `json:"width"`
	Height     int          `json:"height"`
}

// RegisterInternalRoutes mounts the /internal/v1 group. When internalToken is
// non-empty, requests must carry it in X-Internal-Token.
func RegisterInternalRoutes(r *gin.Engine, store ObjectGetter, internalToken string) {
	internal := r.Group("/internal/v1")
	if internalToken != "" {
		internal.Use(func(c *gin.Context) {
			if c.GetHeader("X-Internal-Token") != internalToken {
				response.AbortErr(c, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid internal token.")
				return
			}
			c.Next()
		})
	}
	internal.POST("/detect-edges", detectEdgesHandler(store))
}

func detectEdgesHandler(store ObjectGetter) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req detectRequest
		if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Key) == "" {
			response.BadRequest(c, "INVALID_REQUEST", "A storage key is required.")
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

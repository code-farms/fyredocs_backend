// Package storage is a thin S3-compatible object-storage utility client
// (MinIO/AWS S3/R2). Like shared/queue and shared/natsconn, it carries NO
// business logic: key naming, expiry policy, and plan limits belong to each
// service. Swapping providers is an env-var change only.
package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Config is resolved from environment variables by NewFromEnv.
type Config struct {
	// Endpoint is the internal address services use for data operations,
	// e.g. "minio:9000".
	Endpoint string
	// PublicEndpoint is the origin browsers can reach (e.g. the API gateway,
	// "http://localhost:8080"). Presigned URLs are signed against it. When
	// empty, presigning falls back to Endpoint.
	PublicEndpoint string
	AccessKey      string
	SecretKey      string
	UseSSL         bool
	BucketUploads  string
	BucketOutputs  string
	// Region pins the signing region so presigning never performs a
	// bucket-location network lookup. MinIO ignores it; AWS needs it to match.
	Region string
}

// ConfigFromEnv reads S3_* environment variables.
func ConfigFromEnv() (Config, error) {
	cfg := Config{
		Endpoint:       os.Getenv("S3_ENDPOINT"),
		PublicEndpoint: os.Getenv("S3_PUBLIC_ENDPOINT"),
		AccessKey:      os.Getenv("S3_ACCESS_KEY"),
		SecretKey:      os.Getenv("S3_SECRET_KEY"),
		UseSSL:         strings.EqualFold(os.Getenv("S3_USE_SSL"), "true"),
		BucketUploads:  envOr("S3_BUCKET_UPLOADS", "fyredocs-uploads"),
		BucketOutputs:  envOr("S3_BUCKET_OUTPUTS", "fyredocs-outputs"),
		Region:         envOr("S3_REGION", "us-east-1"),
	}
	if cfg.Endpoint == "" {
		return cfg, fmt.Errorf("storage: S3_ENDPOINT is required")
	}
	if cfg.AccessKey == "" || cfg.SecretKey == "" {
		return cfg, fmt.Errorf("storage: S3_ACCESS_KEY and S3_SECRET_KEY are required")
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ObjectInfo is the subset of object metadata services need.
type ObjectInfo struct {
	Key          string
	Size         int64
	ContentType  string
	LastModified time.Time
	ETag         string
}

// CompletedPart identifies one uploaded part of a multipart upload.
type CompletedPart struct {
	PartNumber int
	ETag       string
}

// IncompleteUpload describes a multipart upload that was never completed.
type IncompleteUpload struct {
	Key       string
	UploadID  string
	Initiated time.Time
}

// Client wraps three minio handles: the internal endpoint for data
// operations, a Core client for raw multipart operations, and a
// public-endpoint client used exclusively for presigning browser-reachable
// URLs (the signature embeds the host, so it must match what the browser
// requests).
type Client struct {
	cfg    Config
	mc     *minio.Client
	core   *minio.Core
	public *minio.Client
}

// New constructs a Client from an explicit Config.
func New(cfg Config) (*Client, error) {
	creds := credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, "")
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}

	mc, err := minio.New(cfg.Endpoint, &minio.Options{Creds: creds, Secure: cfg.UseSSL, Region: region})
	if err != nil {
		return nil, fmt.Errorf("storage: internal client: %w", err)
	}
	core, err := minio.NewCore(cfg.Endpoint, &minio.Options{Creds: creds, Secure: cfg.UseSSL, Region: region})
	if err != nil {
		return nil, fmt.Errorf("storage: core client: %w", err)
	}

	public := mc
	if cfg.PublicEndpoint != "" {
		host, secure, err := splitPublicEndpoint(cfg.PublicEndpoint)
		if err != nil {
			return nil, err
		}
		public, err = minio.New(host, &minio.Options{Creds: creds, Secure: secure, Region: region})
		if err != nil {
			return nil, fmt.Errorf("storage: public client: %w", err)
		}
	}

	return &Client{cfg: cfg, mc: mc, core: core, public: public}, nil
}

// NewFromEnv constructs a Client from S3_* environment variables.
func NewFromEnv() (*Client, error) {
	cfg, err := ConfigFromEnv()
	if err != nil {
		return nil, err
	}
	return New(cfg)
}

// splitPublicEndpoint accepts "http(s)://host[:port]" or bare "host[:port]"
// and returns the host plus whether TLS is in use.
func splitPublicEndpoint(endpoint string) (host string, secure bool, err error) {
	if strings.Contains(endpoint, "://") {
		u, parseErr := url.Parse(endpoint)
		if parseErr != nil {
			return "", false, fmt.Errorf("storage: invalid S3_PUBLIC_ENDPOINT %q: %w", endpoint, parseErr)
		}
		return u.Host, u.Scheme == "https", nil
	}
	return endpoint, false, nil
}

// Config returns the resolved configuration (bucket names etc.).
func (c *Client) Config() Config { return c.cfg }

// BucketUploads returns the uploads bucket name.
func (c *Client) BucketUploads() string { return c.cfg.BucketUploads }

// BucketOutputs returns the outputs bucket name.
func (c *Client) BucketOutputs() string { return c.cfg.BucketOutputs }

// EnsureBucket creates the bucket if it does not already exist.
func (c *Client) EnsureBucket(ctx context.Context, bucket string) error {
	exists, err := c.mc.BucketExists(ctx, bucket)
	if err != nil {
		return fmt.Errorf("storage: bucket check %q: %w", bucket, err)
	}
	if exists {
		return nil
	}
	if err := c.mc.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
		// Lost a create race — treat "already exists" as success.
		if exists, checkErr := c.mc.BucketExists(ctx, bucket); checkErr == nil && exists {
			return nil
		}
		return fmt.Errorf("storage: create bucket %q: %w", bucket, err)
	}
	return nil
}

// PutObject streams r into bucket/key.
func (c *Client) PutObject(ctx context.Context, bucket, key string, r io.Reader, size int64, contentType string) error {
	_, err := c.mc.PutObject(ctx, bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("storage: put %s/%s: %w", bucket, key, err)
	}
	return nil
}

// GetObject opens a streaming reader for bucket/key. Callers must Close it.
func (c *Client) GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	obj, err := c.mc.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("storage: get %s/%s: %w", bucket, key, err)
	}
	return obj, nil
}

// GetObjectRange reads [offset, offset+length) of bucket/key — e.g. the first
// 512 bytes for MIME sniffing.
func (c *Client) GetObjectRange(ctx context.Context, bucket, key string, offset, length int64) ([]byte, error) {
	opts := minio.GetObjectOptions{}
	if err := opts.SetRange(offset, offset+length-1); err != nil {
		return nil, fmt.Errorf("storage: range %s/%s: %w", bucket, key, err)
	}
	obj, err := c.mc.GetObject(ctx, bucket, key, opts)
	if err != nil {
		return nil, fmt.Errorf("storage: get range %s/%s: %w", bucket, key, err)
	}
	defer obj.Close()
	buf, err := io.ReadAll(io.LimitReader(obj, length))
	if err != nil {
		return nil, fmt.Errorf("storage: read range %s/%s: %w", bucket, key, err)
	}
	return buf, nil
}

// StatObject returns object metadata, or an error wrapping ErrNotFound
// semantics from the underlying SDK when the object is missing.
func (c *Client) StatObject(ctx context.Context, bucket, key string) (ObjectInfo, error) {
	info, err := c.mc.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("storage: stat %s/%s: %w", bucket, key, err)
	}
	return ObjectInfo{
		Key:          info.Key,
		Size:         info.Size,
		ContentType:  info.ContentType,
		LastModified: info.LastModified,
		ETag:         info.ETag,
	}, nil
}

// GetObjectSize returns the size in bytes of bucket/key. It is a thin
// convenience wrapper over StatObject for callers that only need the size
// (e.g. capacity/budget checks before downloading into a bounded scratch area).
func (c *Client) GetObjectSize(ctx context.Context, bucket, key string) (int64, error) {
	info, err := c.StatObject(ctx, bucket, key)
	if err != nil {
		return 0, err
	}
	return info.Size, nil
}

// IsNotFound reports whether err represents a missing object/bucket.
func IsNotFound(err error) bool {
	resp := minio.ToErrorResponse(unwrapAll(err))
	return resp.Code == "NoSuchKey" || resp.Code == "NoSuchBucket" || resp.StatusCode == 404
}

func unwrapAll(err error) error {
	for {
		unwrapped := func() error {
			type unwrapper interface{ Unwrap() error }
			if u, ok := err.(unwrapper); ok {
				return u.Unwrap()
			}
			return nil
		}()
		if unwrapped == nil {
			return err
		}
		err = unwrapped
	}
}

// RemoveObject deletes bucket/key. Deleting a missing object is not an error.
func (c *Client) RemoveObject(ctx context.Context, bucket, key string) error {
	if err := c.mc.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{}); err != nil {
		if IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("storage: remove %s/%s: %w", bucket, key, err)
	}
	return nil
}

// PresignGet returns a browser-reachable presigned GET URL. reqParams may set
// response-content-disposition / response-content-type overrides.
func (c *Client) PresignGet(ctx context.Context, bucket, key string, expiry time.Duration, reqParams url.Values) (string, error) {
	u, err := c.public.PresignedGetObject(ctx, bucket, key, expiry, reqParams)
	if err != nil {
		return "", fmt.Errorf("storage: presign get %s/%s: %w", bucket, key, err)
	}
	return u.String(), nil
}

// PresignPut returns a browser-reachable presigned PUT URL for a single
// (non-multipart) object upload.
func (c *Client) PresignPut(ctx context.Context, bucket, key string, expiry time.Duration) (string, error) {
	u, err := c.public.PresignedPutObject(ctx, bucket, key, expiry)
	if err != nil {
		return "", fmt.Errorf("storage: presign put %s/%s: %w", bucket, key, err)
	}
	return u.String(), nil
}

// DownloadToFile streams bucket/key into localPath, creating parent
// directories. Worker helper: subprocess tools need real local files.
func (c *Client) DownloadToFile(ctx context.Context, bucket, key, localPath string) error {
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return fmt.Errorf("storage: mkdir for %s: %w", localPath, err)
	}
	if err := c.mc.FGetObject(ctx, bucket, key, localPath, minio.GetObjectOptions{}); err != nil {
		return fmt.Errorf("storage: download %s/%s: %w", bucket, key, err)
	}
	return nil
}

// UploadFromFile streams localPath into bucket/key and returns the size.
func (c *Client) UploadFromFile(ctx context.Context, bucket, key, localPath, contentType string) (int64, error) {
	info, err := c.mc.FPutObject(ctx, bucket, key, localPath, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return 0, fmt.Errorf("storage: upload %s → %s/%s: %w", localPath, bucket, key, err)
	}
	return info.Size, nil
}

// CopyObject performs a server-side copy of srcBucket/srcKey to
// dstBucket/dstKey. No object bytes pass through this process, so it is far
// cheaper than download+upload — used by workers to materialise a cached
// result under a new job's output key.
func (c *Client) CopyObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string) error {
	src := minio.CopySrcOptions{Bucket: srcBucket, Object: srcKey}
	dst := minio.CopyDestOptions{Bucket: dstBucket, Object: dstKey}
	if _, err := c.mc.CopyObject(ctx, dst, src); err != nil {
		return fmt.Errorf("storage: copy %s/%s → %s/%s: %w", srcBucket, srcKey, dstBucket, dstKey, err)
	}
	return nil
}

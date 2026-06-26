package storage

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestConfigFromEnv(t *testing.T) {
	t.Run("required fields", func(t *testing.T) {
		t.Setenv("S3_ENDPOINT", "")
		t.Setenv("S3_ACCESS_KEY", "")
		t.Setenv("S3_SECRET_KEY", "")
		if _, err := ConfigFromEnv(); err == nil {
			t.Fatal("expected error when S3_ENDPOINT is empty")
		}

		t.Setenv("S3_ENDPOINT", "minio:9000")
		if _, err := ConfigFromEnv(); err == nil {
			t.Fatal("expected error when credentials are empty")
		}
	})

	t.Run("defaults and overrides", func(t *testing.T) {
		t.Setenv("S3_ENDPOINT", "minio:9000")
		t.Setenv("S3_ACCESS_KEY", "ak")
		t.Setenv("S3_SECRET_KEY", "sk")
		t.Setenv("S3_PUBLIC_ENDPOINT", "http://localhost:8080")
		t.Setenv("S3_USE_SSL", "false")
		t.Setenv("S3_BUCKET_UPLOADS", "")
		t.Setenv("S3_BUCKET_OUTPUTS", "custom-outputs")

		cfg, err := ConfigFromEnv()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.BucketUploads != "fyredocs-uploads" {
			t.Errorf("expected default uploads bucket, got %q", cfg.BucketUploads)
		}
		if cfg.BucketOutputs != "custom-outputs" {
			t.Errorf("expected custom outputs bucket, got %q", cfg.BucketOutputs)
		}
		if cfg.UseSSL {
			t.Error("expected UseSSL=false")
		}
	})
}

func TestSplitPublicEndpoint(t *testing.T) {
	tests := []struct {
		in     string
		host   string
		secure bool
		err    bool
	}{
		{"http://localhost:8080", "localhost:8080", false, false},
		{"https://files.fyredocs.com", "files.fyredocs.com", true, false},
		{"minio:9000", "minio:9000", false, false},
		{"http://[::1]:8080", "[::1]:8080", false, false},
	}
	for _, tt := range tests {
		host, secure, err := splitPublicEndpoint(tt.in)
		if (err != nil) != tt.err {
			t.Errorf("%q: unexpected error state: %v", tt.in, err)
			continue
		}
		if host != tt.host || secure != tt.secure {
			t.Errorf("%q: got (%q, %v), want (%q, %v)", tt.in, host, secure, tt.host, tt.secure)
		}
	}
}

func TestNewBuildsPresignClients(t *testing.T) {
	cfg := Config{
		Endpoint:       "minio:9000",
		PublicEndpoint: "http://localhost:8080",
		AccessKey:      "ak",
		SecretKey:      "sk",
		BucketUploads:  "fyredocs-uploads",
		BucketOutputs:  "fyredocs-outputs",
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.BucketUploads() != "fyredocs-uploads" || c.BucketOutputs() != "fyredocs-outputs" {
		t.Error("bucket accessors mismatch")
	}

	// Presigned URLs must be signed against the PUBLIC endpoint host.
	u, err := c.PresignUploadPart(context.Background(), "fyredocs-uploads", "uploads/u1/file.pdf", "s3id", 1, time.Minute)
	if err != nil {
		t.Fatalf("PresignUploadPart: %v", err)
	}
	if !strings.Contains(u, "localhost:8080") {
		t.Errorf("presigned URL not signed against public endpoint: %s", u)
	}
	if !strings.Contains(u, "partNumber=1") || !strings.Contains(u, "uploadId=s3id") {
		t.Errorf("presigned URL missing multipart params: %s", u)
	}
}

// Integration tests run only when a live S3 endpoint is provided.
func TestIntegration(t *testing.T) {
	endpoint := os.Getenv("S3_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("S3_TEST_ENDPOINT not set; skipping integration tests")
	}
	cfg := Config{
		Endpoint:      endpoint,
		AccessKey:     os.Getenv("S3_TEST_ACCESS_KEY"),
		SecretKey:     os.Getenv("S3_TEST_SECRET_KEY"),
		BucketUploads: "fyredocs-test",
		BucketOutputs: "fyredocs-test",
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	if err := c.EnsureBucket(ctx, cfg.BucketUploads); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}

	key := "it/test-object.txt"
	body := []byte("hello fyredocs")
	if err := c.PutObject(ctx, cfg.BucketUploads, key, bytes.NewReader(body), int64(len(body)), "text/plain"); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	info, err := c.StatObject(ctx, cfg.BucketUploads, key)
	if err != nil || info.Size != int64(len(body)) {
		t.Fatalf("StatObject: %v size=%d", err, info.Size)
	}
	head, err := c.GetObjectRange(ctx, cfg.BucketUploads, key, 0, 5)
	if err != nil || string(head) != "hello" {
		t.Fatalf("GetObjectRange: %v %q", err, head)
	}

	// Server-side copy must produce an identical object at the destination key.
	copyKey := "it/test-object-copy.txt"
	if err := c.CopyObject(ctx, cfg.BucketUploads, key, cfg.BucketOutputs, copyKey); err != nil {
		t.Fatalf("CopyObject: %v", err)
	}
	cinfo, err := c.StatObject(ctx, cfg.BucketOutputs, copyKey)
	if err != nil || cinfo.Size != int64(len(body)) {
		t.Fatalf("StatObject(copy): %v size=%d", err, cinfo.Size)
	}
	if err := c.RemoveObject(ctx, cfg.BucketOutputs, copyKey); err != nil {
		t.Fatalf("RemoveObject(copy): %v", err)
	}

	if err := c.RemoveObject(ctx, cfg.BucketUploads, key); err != nil {
		t.Fatalf("RemoveObject: %v", err)
	}
	if _, err := c.StatObject(ctx, cfg.BucketUploads, key); !IsNotFound(err) {
		t.Fatalf("expected not-found after delete, got %v", err)
	}
}

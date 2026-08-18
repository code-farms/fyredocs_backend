package storage

import (
	"context"
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
		if cfg.BucketUploads != "uploads" {
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
		BucketUploads:  "uploads",
		BucketOutputs:  "outputs",
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.BucketUploads() != "uploads" || c.BucketOutputs() != "outputs" {
		t.Error("bucket accessors mismatch")
	}

	// Presigned URLs must be signed against the PUBLIC endpoint host.
	u, err := c.PresignUploadPart(context.Background(), "uploads", "uploads/u1/file.pdf", "s3id", 1, time.Minute)
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

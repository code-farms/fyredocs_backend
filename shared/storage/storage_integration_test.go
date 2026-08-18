//go:build integration

// Object-storage round-trip against a real MinIO (the essentials stack).
// Migrated from the pre-taxonomy S3_TEST_ENDPOINT t.Skip gate so it runs as
// part of `make test-integration` instead of silently passing when nobody
// remembered the env var.
package storage

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

// integrationConfig resolves the endpoint/credentials for the tagged run.
// S3_TEST_* keep working as explicit overrides; otherwise the stack's own
// S3_* values are used (make test-integration sources .env), with the
// compose-default endpoint as the last fallback. Tagged tests fail loudly on
// missing credentials — a silent skip is how the old gate rotted.
func integrationConfig(t *testing.T) Config {
	t.Helper()
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v := strings.TrimSpace(os.Getenv(k)); v != "" {
				return v
			}
		}
		return ""
	}
	// The stack's app credentials are policy-scoped to the real buckets, so
	// the test uses those (under an it/ key prefix) rather than a dedicated
	// test bucket the policy would deny — which also means the test runs with
	// exactly the permissions production code has.
	cfg := Config{
		Endpoint:      pick("S3_TEST_ENDPOINT", "S3_ENDPOINT"),
		AccessKey:     pick("S3_TEST_ACCESS_KEY", "S3_ACCESS_KEY"),
		SecretKey:     pick("S3_TEST_SECRET_KEY", "S3_SECRET_KEY"),
		BucketUploads: pick("S3_BUCKET_UPLOADS"),
		BucketOutputs: pick("S3_BUCKET_OUTPUTS"),
	}
	if cfg.BucketUploads == "" {
		cfg.BucketUploads = "uploads"
	}
	if cfg.BucketOutputs == "" {
		cfg.BucketOutputs = "outputs"
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = "localhost:9000"
	}
	// Compose publishes MinIO on the loopback only; a docker-internal
	// hostname in .env (minio:9000) is unreachable from the host runner.
	if strings.HasPrefix(cfg.Endpoint, "minio:") {
		cfg.Endpoint = "localhost:9000"
	}
	if cfg.AccessKey == "" || cfg.SecretKey == "" {
		t.Fatal("storage integration: no S3 credentials in env — run via `make test-integration` (sources .env) with the essentials stack up")
	}
	return cfg
}

func TestIntegration(t *testing.T) {
	cfg := integrationConfig(t)
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

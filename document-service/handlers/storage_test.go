// Test double for the ObjectStore seam plus tests for the swap itself
// (modelled on job-service/handlers/fakestore_test.go, duplicated by design —
// cross-service test helpers are forbidden, see testing-strategy.md).
//
// The handler halves that USE the store (DownloadDocument's presign redirect,
// PurgeDocument's backup-object removal) all fetch the document row first, so
// they cannot be exercised offline; they belong to the integration tier. What
// CAN be pinned here is the injection contract those tests will rely on.
package handlers

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"testing"
	"time"
)

// fakeObjectStore is an in-memory ObjectStore recording mutating calls so
// tests can assert on side effects, with targeted failure injection.
type fakeObjectStore struct {
	mu sync.Mutex

	// removed collects "bucket/key" for every RemoveObject call.
	removed []string
	// presignErr, when set, is returned by PresignGet.
	presignErr error
	// removeErr, when set, is returned by RemoveObject.
	removeErr error
}

// Compile-time pin: the fake must keep satisfying the interface, so a change
// to ObjectStore breaks this file before it breaks a future integration wave.
var _ ObjectStore = (*fakeObjectStore)(nil)

func (f *fakeObjectStore) BucketOutputs() string { return "test-outputs" }

func (f *fakeObjectStore) PresignGet(_ context.Context, bucket, key string, _ time.Duration, reqParams url.Values) (string, error) {
	if f.presignErr != nil {
		return "", f.presignErr
	}
	return fmt.Sprintf("http://minio.test/%s/%s?%s", bucket, key, reqParams.Encode()), nil
}

func (f *fakeObjectStore) RemoveObject(_ context.Context, bucket, key string) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, bucket+"/"+key)
	return nil
}

// withFakeObjectStore swaps the package-level objStore for a fake for the
// test's duration, restoring the previous value in Cleanup (the canonical
// global-swap shape from testing-strategy.md).
func withFakeObjectStore(t *testing.T) *fakeObjectStore {
	t.Helper()
	fs := &fakeObjectStore{}
	prev := objStore
	SetObjectStore(fs)
	t.Cleanup(func() { SetObjectStore(prev) })
	return fs
}

// TestSetObjectStoreSwapAndRestore pins the injection seam main.go and the
// tests share: SetObjectStore replaces the package global, and the Cleanup
// restore puts the previous store back — without which one test's fake would
// leak into every later test in the package.
func TestSetObjectStoreSwapAndRestore(t *testing.T) {
	orig := objStore
	var fs *fakeObjectStore
	t.Run("swap", func(t *testing.T) {
		fs = withFakeObjectStore(t)
		if objStore != ObjectStore(fs) {
			t.Fatal("SetObjectStore did not install the fake")
		}
		got, err := objStore.PresignGet(context.Background(), objStore.BucketOutputs(), "k.pdf", time.Minute, url.Values{"a": {"b"}})
		if err != nil || got != "http://minio.test/test-outputs/k.pdf?a=b" {
			t.Errorf("PresignGet = (%q, %v)", got, err)
		}
		if err := objStore.RemoveObject(context.Background(), "test-outputs", "k.pdf"); err != nil {
			t.Errorf("RemoveObject: %v", err)
		}
	})
	if objStore != orig {
		t.Error("Cleanup did not restore the previous objStore")
	}
	if len(fs.removed) != 1 || fs.removed[0] != "test-outputs/k.pdf" {
		t.Errorf("removed = %v, want the one recorded key", fs.removed)
	}
}

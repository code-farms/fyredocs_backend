package handlers

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"sync"
	"time"

	"fyredocs/shared/storage"
)

// fakeStore is an in-memory ObjectStore used by handler tests. It records
// every mutating call so tests can assert on side effects (objects removed,
// multipart uploads aborted, ...) and supports targeted failure injection.
type fakeStore struct {
	mu sync.Mutex

	objects    map[string][]byte // "bucket/key" -> data
	multiparts map[string]*fakeMultipart
	nextMpID   int

	removed []string // "bucket/key" of every RemoveObject call
	aborted []string // s3UploadIDs of every AbortMultipart call

	// completeSize is the object size materialised by CompleteMultipart
	// (parts carry no bytes in the fake). Defaults to 1024.
	completeSize int64
	// completeErr, when set, is returned by CompleteMultipart.
	completeErr error
	// statErr, when set, is returned by StatObject.
	statErr error
}

type fakeMultipart struct {
	bucket, key, contentType string
	completed                bool
	aborted                  bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		objects:      map[string][]byte{},
		multiparts:   map[string]*fakeMultipart{},
		completeSize: 1024,
	}
}

// withFakeStore swaps the package-level objStore for an in-memory fake for
// the duration of the test, then restores the original.
func withFakeStore(t interface {
	Helper()
	Cleanup(func())
}) *fakeStore {
	t.Helper()
	fs := newFakeStore()
	prev := objStore
	objStore = fs
	t.Cleanup(func() { objStore = prev })
	return fs
}

// objKey is the map index for an object: "bucket/key".
func objKey(bucket, key string) string { return bucket + "/" + key }

func (f *fakeStore) putObjectBytes(bucket, key string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[objKey(bucket, key)] = data
}

func (f *fakeStore) hasObject(bucket, key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.objects[objKey(bucket, key)]
	return ok
}

func (f *fakeStore) BucketUploads() string { return "test-uploads" }
func (f *fakeStore) BucketOutputs() string { return "test-outputs" }

func (f *fakeStore) PutObject(_ context.Context, bucket, key string, r io.Reader, _ int64, _ string) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.putObjectBytes(bucket, key, data)
	return nil
}

func (f *fakeStore) GetObjectRange(_ context.Context, bucket, key string, offset, length int64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.objects[objKey(bucket, key)]
	if !ok {
		return nil, fmt.Errorf("fakeStore: object %s/%s not found", bucket, key)
	}
	if offset >= int64(len(data)) {
		return nil, nil
	}
	end := offset + length
	if end > int64(len(data)) {
		end = int64(len(data))
	}
	return data[offset:end], nil
}

func (f *fakeStore) StatObject(_ context.Context, bucket, key string) (storage.ObjectInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.statErr != nil {
		return storage.ObjectInfo{}, f.statErr
	}
	data, ok := f.objects[objKey(bucket, key)]
	if !ok {
		return storage.ObjectInfo{}, fmt.Errorf("fakeStore: object %s/%s not found", bucket, key)
	}
	return storage.ObjectInfo{Key: key, Size: int64(len(data)), LastModified: time.Now()}, nil
}

func (f *fakeStore) RemoveObject(_ context.Context, bucket, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, objKey(bucket, key))
	f.removed = append(f.removed, objKey(bucket, key))
	return nil
}

func (f *fakeStore) PresignGet(_ context.Context, bucket, key string, _ time.Duration, reqParams url.Values) (string, error) {
	return fmt.Sprintf("http://minio.test/%s/%s?%s", bucket, key, reqParams.Encode()), nil
}

func (f *fakeStore) CreateMultipart(_ context.Context, bucket, key, contentType string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextMpID++
	id := fmt.Sprintf("mpu-%d", f.nextMpID)
	f.multiparts[id] = &fakeMultipart{bucket: bucket, key: key, contentType: contentType}
	return id, nil
}

func (f *fakeStore) PresignUploadPart(_ context.Context, bucket, key, s3UploadID string, partNumber int, _ time.Duration) (string, error) {
	return fmt.Sprintf("http://minio.test/%s/%s?uploadId=%s&partNumber=%d", bucket, key, s3UploadID, partNumber), nil
}

func (f *fakeStore) CompleteMultipart(_ context.Context, bucket, key, s3UploadID string, parts []storage.CompletedPart) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.completeErr != nil {
		return f.completeErr
	}
	mp, ok := f.multiparts[s3UploadID]
	if !ok {
		return fmt.Errorf("fakeStore: unknown multipart upload %q", s3UploadID)
	}
	if len(parts) == 0 {
		return fmt.Errorf("fakeStore: no parts supplied")
	}
	mp.completed = true
	f.objects[objKey(bucket, key)] = make([]byte, f.completeSize)
	return nil
}

func (f *fakeStore) AbortMultipart(_ context.Context, _, _ string, s3UploadID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if mp, ok := f.multiparts[s3UploadID]; ok {
		mp.aborted = true
	}
	f.aborted = append(f.aborted, s3UploadID)
	return nil
}

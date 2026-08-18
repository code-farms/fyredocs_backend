// Tests for the storage injection seam. storage.go is otherwise pure
// declaration: the ObjectStore interface itself is exercised end-to-end by
// the fakeStore-backed handler tests (fakestore_test.go and its users), and
// bucketFor is covered in jobs_test.go.
package handlers

import "testing"

// fakeStore must keep satisfying the exact interface the handlers consume;
// this fails at compile time if ObjectStore grows a method the fake lacks.
var _ ObjectStore = (*fakeStore)(nil)

func TestSetObjectStore(t *testing.T) {
	prev := objStore
	t.Cleanup(func() { objStore = prev })

	fs := newFakeStore()
	SetObjectStore(fs)
	if objStore != ObjectStore(fs) {
		t.Error("SetObjectStore did not install the given store")
	}

	// main() only calls this once at boot, but a nil install must land as nil
	// so the handlers' nil-store guards (500, not panic) actually engage.
	SetObjectStore(nil)
	if objStore != nil {
		t.Error("SetObjectStore(nil) must clear the store")
	}
}

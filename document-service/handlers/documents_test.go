// Tests for document handlers' pure gates. The invariant under guard: the
// backups/ storage prefix is an ownership assertion — a caller can never
// register another user's backup object as their own document.
package handlers

import (
	"testing"

	"github.com/google/uuid"
)

func TestStoragePathAllowed(t *testing.T) {
	me := uuid.Must(uuid.NewV7())
	other := uuid.Must(uuid.NewV7())

	tests := []struct {
		name string
		path string
		want bool
	}{
		{"empty passes (metadata-only rows)", "", true},
		{"job-output shape passes untouched", "outputs/abc/file.pdf", true},
		{"own backup prefix passes", "backups/" + me.String() + "/x/file.pdf", true},
		{"someone else's backup rejected", "backups/" + other.String() + "/x/file.pdf", false},
		{"bare backups prefix rejected", "backups/", false},
		{"prefix truncation trick rejected", "backups/" + me.String()[:8] + "/f.pdf", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := storagePathAllowed(tt.path, me); got != tt.want {
				t.Errorf("storagePathAllowed(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

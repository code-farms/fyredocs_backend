package corpus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// LockEntry pins one generated file.
type LockEntry struct {
	Path     string `json:"path"` // relative to the corpus output directory
	SHA256   string `json:"sha256"`
	Bytes    int64  `json:"bytes"`
	Category string `json:"category"`
}

// Lock maps item ID to its pinned file.
type Lock map[string]LockEntry

// LockFile is the lock's name inside the output directory.
const LockFile = "corpus.lock"

// HashFile returns the hex sha256 and size of path.
func HashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// WriteLock writes the lock into dir with sorted keys, so two locks of the
// same corpus diff cleanly.
func WriteLock(dir string, l Lock) error {
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, LockFile), append(b, '\n'), 0o644)
}

// ReadLock reads the lock from dir.
func ReadLock(dir string) (Lock, error) {
	b, err := os.ReadFile(filepath.Join(dir, LockFile))
	if err != nil {
		return nil, err
	}
	var l Lock
	if err := json.Unmarshal(b, &l); err != nil {
		return nil, fmt.Errorf("%s: %w", LockFile, err)
	}
	return l, nil
}

// LockSHA is the sha256 of the lock file itself: one value that identifies the
// exact corpus a report was measured on (doc 16 §5.4 corpus_manifest_sha).
func LockSHA(dir string) (string, error) {
	s, _, err := HashFile(filepath.Join(dir, LockFile))
	return s, err
}

// Verify re-hashes every locked file and checks that every manifest item is
// locked. It returns all problems, not just the first, so one run lists every
// file to regenerate.
func Verify(dir string, l Lock, items []Item) []error {
	var errs []error
	ids := make([]string, 0, len(l))
	for id := range l {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		e := l[id]
		sum, n, err := HashFile(filepath.Join(dir, e.Path))
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
		case sum != e.SHA256 || n != e.Bytes:
			errs = append(errs, fmt.Errorf("%s: %s changed since gen (sha256 or size differs)", id, e.Path))
		}
	}
	for _, it := range items {
		if _, ok := l[it.ID]; !ok {
			errs = append(errs, fmt.Errorf("%s: in the manifest but not generated (run gen)", it.ID))
		}
	}
	return errs
}

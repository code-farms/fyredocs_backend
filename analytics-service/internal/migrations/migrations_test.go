package migrations

import (
	"io/fs"
	"regexp"
	"strconv"
	"testing"
)

// Migration files must be numbered sequentially from 1 with no gaps or
// duplicates: goose applies them in numeric order, and a gap means a migration
// that was written but never committed (or was renumbered), which silently
// changes what a fresh database gets versus production.
var namePattern = regexp.MustCompile(`^(\d{5})_[a-z0-9_]+\.sql$`)

func TestMigrationsAreEmbeddedAndSequential(t *testing.T) {
	entries, err := fs.Glob(FS, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no migrations embedded — check the //go:embed directive")
	}

	seen := map[int]string{}
	for _, name := range entries {
		m := namePattern.FindStringSubmatch(name)
		if m == nil {
			t.Errorf("migration %q does not match NNNNN_snake_case.sql", name)
			continue
		}
		version, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatal(err)
		}
		if prev, dup := seen[version]; dup {
			t.Errorf("duplicate migration version %d: %s and %s", version, prev, name)
		}
		seen[version] = name
	}

	for v := 1; v <= len(seen); v++ {
		if _, ok := seen[v]; !ok {
			t.Errorf("missing migration version %d (versions must be contiguous from 1)", v)
		}
	}
}

func TestMigrationsDeclareUpAndDown(t *testing.T) {
	entries, _ := fs.Glob(FS, "*.sql")
	for _, name := range entries {
		body, err := fs.ReadFile(FS, name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		for _, marker := range []string{"-- +goose Up", "-- +goose Down"} {
			if !contains(text, marker) {
				t.Errorf("%s is missing %q", name, marker)
			}
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

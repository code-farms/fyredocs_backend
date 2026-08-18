// Fuzz targets for the activity-ingest sanitizers. Both process raw client
// bytes on an unauthenticated endpoint; the invariants are containment: never
// panic, never store invalid JSON, and never let a sensitive key through.
package handlers

import (
	"encoding/json"
	"testing"

	"analytics-service/internal/activity"
)

func FuzzBoundedJSON(f *testing.F) {
	for _, seed := range [][]byte{
		nil, []byte(`{}`), []byte(`"x"`), []byte(`{"a":1}`),
		[]byte(`not json`), []byte(`{"a":`), []byte(`[1,2,`),
		[]byte(`"` + string(make([]byte, 2048)) + `"`),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		out := boundedJSON(raw)
		if out == nil {
			return
		}
		if len(out) > maxValueBytes {
			t.Errorf("boundedJSON let %d bytes through (cap %d)", len(out), maxValueBytes)
		}
		if !json.Valid(out) {
			t.Errorf("boundedJSON stored invalid JSON: %q", out)
		}
	})
}

func FuzzSanitizeMetadata(f *testing.F) {
	f.Add("password", []byte(`"x"`))
	f.Add("pageCount", []byte(`3`))
	f.Add("PASSWORD", []byte(`"x"`))
	f.Add("api_key", []byte(`"k"`))
	f.Add("note", []byte(`not json`))
	f.Add("", []byte(``))
	f.Add("promptText", []byte(`"p"`))
	f.Fuzz(func(t *testing.T, key string, val []byte) {
		out := sanitizeMetadata(map[string]json.RawMessage{key: json.RawMessage(val)})
		if out == nil {
			return
		}
		if activity.SensitiveMetadataKey(key) {
			t.Errorf("sensitive key %q survived sanitization: %s", key, out)
		}
		if !json.Valid([]byte(out)) {
			t.Errorf("sanitizer stored invalid JSON: %s", out)
		}
	})
}

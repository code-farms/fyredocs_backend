package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"fyredocs/shared/response"
)

func TestParseSimHash(t *testing.T) {
	v, err := parseSimHash("ffffffffffffffff")
	if err != nil || v != -1 {
		t.Fatalf("all-ones = %d, %v; want -1 (signed reinterpretation)", v, err)
	}
	if v, err := parseSimHash(" 00000000000000Ab "); err != nil || v != 0xab {
		t.Errorf("mixed case = %d, %v", v, err)
	}
	for _, bad := range []string{"", "abc", "zzzzzzzzzzzzzzzz", "0123456789abcdef0"} {
		if _, err := parseSimHash(bad); err == nil {
			t.Errorf("parseSimHash(%q) accepted", bad)
		}
	}
}

func TestHamming(t *testing.T) {
	if d := hamming(0, 0b1011); d != 3 {
		t.Errorf("hamming = %d, want 3", d)
	}
	if d := hamming(-1, 0); d != 64 {
		t.Errorf("hamming(all-ones, 0) = %d, want 64", d)
	}
}

func TestNormalizeExtraction(t *testing.T) {
	if ex, err := normalizeExtraction("receipt", json.RawMessage(`{"docType":"receipt","fields":{"total":12}}`)); err != nil || ex == nil {
		t.Fatalf("receipt object rejected: %v", err)
	}
	if ex, err := normalizeExtraction("receipt", json.RawMessage(` null `)); err != nil || ex != nil {
		t.Errorf("null should mean no extraction, got %s, %v", ex, err)
	}
	if _, err := normalizeExtraction("id_document", json.RawMessage(`{"fields":{"documentNumber":"1234"}}`)); !errors.Is(err, errIDExtraction) {
		t.Errorf("id_document extraction must be refused, got %v", err)
	}
	if _, err := normalizeExtraction("invoice", json.RawMessage(`[1,2]`)); err == nil {
		t.Error("non-object accepted")
	}
	big := `{"x":"` + strings.Repeat("a", maxExtractionBytes) + `"}`
	if _, err := normalizeExtraction("invoice", json.RawMessage(big)); err == nil {
		t.Error("oversized extraction accepted")
	}
}

func TestNearMatches(t *testing.T) {
	target := int64(0)
	sim := func(v int64) *int64 { return &v }
	a, b, c, d := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	got := nearMatches(target, []candidate{
		{ID: a, Name: "far", SimHash: sim(0b1111)},   // 4 bits — out
		{ID: b, Name: "two", SimHash: sim(0b11)},     // 2
		{ID: c, Name: "skip", SimHash: sim(0)},       // exact match, skipped
		{ID: d, Name: "one", SimHash: sim(0b100000)}, // 1
		{ID: uuid.New(), Name: "nil"},
	}, map[uuid.UUID]bool{c: true})
	if len(got) != 2 || got[0].Name != "one" || got[1].Name != "two" || *got[0].Distance != 1 {
		t.Fatalf("nearMatches = %+v, want [one(1) two(2)]", got)
	}
}

func TestParseAIFields(t *testing.T) {
	str := func(s string) *string { return &s }
	dt, ex, sh, msg := parseAIFields(str("receipt"), json.RawMessage(`{"fields":{}}`), str("000000000000000f"))
	if msg != "" || *dt != "receipt" || ex == nil || *sh != 15 {
		t.Fatalf("valid fields: %v %s %v %q", dt, ex, sh, msg)
	}
	if _, _, _, msg := parseAIFields(nil, json.RawMessage(`{"a":1}`), nil); msg == "" {
		t.Error("extraction without docType accepted")
	}
	if _, _, _, msg := parseAIFields(str("passport"), nil, nil); msg == "" {
		t.Error("unknown docType accepted")
	}
	if dt, ex, _, msg := parseAIFields(str("id_document"), nil, nil); msg != "" || *dt != "id_document" || ex != nil {
		t.Errorf("id_document docType alone must be allowed: %q", msg)
	}
	if _, _, _, msg := parseAIFields(str("id_document"), json.RawMessage(`{"fields":{}}`), nil); !strings.Contains(msg, "ID document") {
		t.Errorf("id_document extraction message = %q", msg)
	}
	if _, _, _, msg := parseAIFields(nil, nil, str("xyz")); msg == "" {
		t.Error("bad simhash accepted")
	}
}

func TestSHA256Object(t *testing.T) {
	store := &fakeObjectStore{objects: map[string][]byte{"backups/u/a.pdf": []byte("%PDF-1.7 hello")}}
	got, err := sha256Object(context.Background(), store, "test-outputs", "backups/u/a.pdf")
	want := sha256.Sum256([]byte("%PDF-1.7 hello"))
	if err != nil || got != hex.EncodeToString(want[:]) {
		t.Fatalf("sha256Object = %q, %v", got, err)
	}
	if _, err := sha256Object(context.Background(), store, "test-outputs", "missing"); err == nil {
		t.Error("missing object should error")
	}
}

func TestHashDocumentAsyncNoopsWithoutStoreOrPath(t *testing.T) {
	prev := objStore
	t.Cleanup(func() { objStore = prev })
	objStore = nil
	HashDocumentAsync(uuid.New(), "backups/x.pdf") // must not panic on nil store / nil DB
	objStore = &fakeObjectStore{}
	HashDocumentAsync(uuid.New(), "") // no path → nothing to hash
}

func TestPutExtractionGuards(t *testing.T) {
	uid := uuid.New()
	pattern := "/documents/:id/extraction"
	cases := []struct {
		name, target, body, code string
	}{
		{"bad id", "/documents/nope/extraction", `{"docType":"receipt"}`, response.CodeInvalidID},
		{"unknown type", "/documents/" + uuid.NewString() + "/extraction", `{"docType":"passport"}`, response.CodeInvalidInput},
		{"id details refused", "/documents/" + uuid.NewString() + "/extraction",
			`{"docType":"id_document","extraction":{"fields":{"documentNumber":"1"}}}`, response.CodeInvalidInput},
		{"non-object", "/documents/" + uuid.NewString() + "/extraction", `{"docType":"receipt","extraction":"x"}`, response.CodeInvalidInput},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doAuthed(t, uid, http.MethodPut, pattern, PutExtraction, tc.target, tc.body)
			wantErrEnvelope(t, w, http.StatusBadRequest, tc.code)
		})
	}
}

func TestDuplicatesAndListGuards(t *testing.T) {
	uid := uuid.New()
	w := doAuthed(t, uid, http.MethodGet, "/documents/:id/duplicates", GetDuplicates, "/documents/nope/duplicates", "")
	wantErrEnvelope(t, w, http.StatusBadRequest, response.CodeInvalidID)

	w = doAuthed(t, uid, http.MethodGet, "/documents", ListDocuments, "/documents?docType=passport", "")
	wantErrEnvelope(t, w, http.StatusBadRequest, response.CodeInvalidInput)
}

func TestCreateAndUpdateRejectBadAIFields(t *testing.T) {
	uid := uuid.New()
	w := doAuthed(t, uid, http.MethodPost, "/documents", CreateDocument, "/documents",
		`{"name":"a.pdf","docType":"receipt","simhash":"nothex"}`)
	wantErrEnvelope(t, w, http.StatusBadRequest, response.CodeInvalidInput)

	w = doAuthed(t, uid, http.MethodPost, "/documents", CreateDocument, "/documents",
		`{"name":"id.pdf","docType":"id_document","extraction":{"fields":{"documentNumber":"1"}}}`)
	wantErrEnvelope(t, w, http.StatusBadRequest, response.CodeInvalidInput)

	w = doAuthed(t, uid, http.MethodPatch, "/documents/:id", UpdateDocument, "/documents/"+uuid.NewString(), `{"simhash":"123"}`)
	wantErrEnvelope(t, w, http.StatusBadRequest, response.CodeInvalidInput)
}

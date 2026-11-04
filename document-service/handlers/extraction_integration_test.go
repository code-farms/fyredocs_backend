//go:build integration

// Integration tests for the AI extraction + duplicate-detection surface
// (migration 00004, extraction.go, hashing.go) against the real schema. Same
// Truncate-isolated harness as db_integration_test.go.
package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"document-service/internal/models"
)

type dupPayload struct {
	Exact []struct {
		ID   uuid.UUID `json:"id"`
		Name string    `json:"name"`
	} `json:"exact"`
	Near []struct {
		ID       uuid.UUID `json:"id"`
		Distance int       `json:"distance"`
	} `json:"near"`
	Pending bool `json:"pending"`
}

func TestExtraction_SaveAndFilterReceipts(t *testing.T) {
	withDB(t)
	uid := uuid.New()
	receipt := createDoc(t, uid, `{"name":"cafe.pdf"}`)
	createDoc(t, uid, `{"name":"plain.pdf"}`)
	// Created with the AI fields directly — the mobile backup path.
	createDoc(t, uid, `{"name":"lunch.pdf","docType":"receipt","extraction":{"fields":{"total":90}},"simhash":"00000000000000ff"}`)

	w := doAuthed(t, uid, http.MethodPut, "/documents/:id/extraction", PutExtraction,
		"/documents/"+receipt.ID.String()+"/extraction", `{"docType":"receipt","extraction":{"fields":{"total":240,"currency":"INR"}}}`)
	wantStatus(t, w, http.StatusOK)

	row, _ := reloadDoc(t, receipt.ID)
	if row.DocType == nil || *row.DocType != "receipt" || string(row.Extraction) == "" {
		t.Fatalf("extraction not persisted: %v %s", row.DocType, row.Extraction)
	}

	docs, meta := listDocs(t, uid, "?docType=receipt")
	if meta.Total != 2 || len(docs) != 2 {
		t.Fatalf("receipts = %d (total %d), want 2", len(docs), meta.Total)
	}
}

func TestExtraction_IDDocumentStoresTypeOnly(t *testing.T) {
	withDB(t)
	uid := uuid.New()
	doc := createDoc(t, uid, `{"name":"aadhaar.pdf"}`)
	path := "/documents/" + doc.ID.String() + "/extraction"
	w := doAuthed(t, uid, http.MethodPut, "/documents/:id/extraction", PutExtraction, path, `{"docType":"id_document"}`)
	wantStatus(t, w, http.StatusOK)
	row, _ := reloadDoc(t, doc.ID)
	if row.Extraction != nil {
		t.Errorf("id_document extraction stored: %s", row.Extraction)
	}
}

func TestExtraction_OtherUsersDocumentIs404(t *testing.T) {
	withDB(t)
	doc := createDoc(t, uuid.New(), `{"name":"theirs.pdf"}`)
	w := doAuthed(t, uuid.New(), http.MethodPut, "/documents/:id/extraction", PutExtraction,
		"/documents/"+doc.ID.String()+"/extraction", `{"docType":"receipt"}`)
	wantStatus(t, w, http.StatusNotFound)
}

func TestDuplicates_ExactNearAndScope(t *testing.T) {
	withDB(t)
	uid := uuid.New()
	a := createDoc(t, uid, `{"name":"lease.pdf","simhash":"0000000000000000"}`)
	b := createDoc(t, uid, `{"name":"lease copy.pdf","simhash":"0000000000000000"}`)
	c := createDoc(t, uid, `{"name":"lease rescan.pdf","simhash":"0000000000000007"}`) // 3 bits
	createDoc(t, uid, `{"name":"other.pdf","simhash":"000000000000000f"}`)             // 4 bits — out
	stranger := createDoc(t, uuid.New(), `{"name":"same bytes, other user.pdf","simhash":"0000000000000000"}`)

	hash := hex.EncodeToString(func() []byte { s := sha256.Sum256([]byte("pdf")); return s[:] }())
	for _, id := range []uuid.UUID{a.ID, b.ID, stranger.ID} {
		if err := models.DB.Model(&models.Document{}).Where("id = ?", id).UpdateColumn("content_sha256", hash).Error; err != nil {
			t.Fatal(err)
		}
	}

	w := doAuthed(t, uid, http.MethodGet, "/documents/:id/duplicates", GetDuplicates, "/documents/"+a.ID.String()+"/duplicates", "")
	wantStatus(t, w, http.StatusOK)
	got, _ := decodeData[dupPayload](t, w)
	if len(got.Exact) != 1 || got.Exact[0].ID != b.ID {
		t.Errorf("exact = %+v, want only %s (other user's identical file must not leak)", got.Exact, b.ID)
	}
	if len(got.Near) != 1 || got.Near[0].ID != c.ID || got.Near[0].Distance != 3 {
		t.Errorf("near = %+v, want only %s at distance 3 (exact match not repeated)", got.Near, c.ID)
	}
	if got.Pending {
		t.Error("pending = true for a hashed document")
	}
}

func TestDuplicates_UnhashedIsPendingAndHashingKeepsUpdatedAt(t *testing.T) {
	withDB(t)
	prev := objStore
	t.Cleanup(func() { objStore = prev })
	uid := uuid.New()
	key := "backups/" + uid.String() + "/x/scan.pdf"
	objStore = &fakeObjectStore{objects: map[string][]byte{key: []byte("%PDF-1.7 scan")}}

	doc := createDoc(t, uid, `{"name":"scan.pdf","storagePath":"`+key+`"}`)
	before, _ := reloadDoc(t, doc.ID)

	want := sha256.Sum256([]byte("%PDF-1.7 scan"))
	deadline := time.Now().Add(5 * time.Second)
	var row models.Document
	for time.Now().Before(deadline) {
		row, _ = reloadDoc(t, doc.ID)
		if row.ContentSHA256 != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if row.ContentSHA256 == nil || *row.ContentSHA256 != hex.EncodeToString(want[:]) {
		t.Fatalf("content_sha256 = %v, want the stored object's hash", row.ContentSHA256)
	}
	if !row.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("hashing bumped updated_at (%s → %s); delta sync would re-pull", before.UpdatedAt, row.UpdatedAt)
	}

	// A row with no hash yet answers pending and starts one.
	other := createDoc(t, uid, `{"name":"later.pdf"}`)
	w := doAuthed(t, uid, http.MethodGet, "/documents/:id/duplicates", GetDuplicates, "/documents/"+other.ID.String()+"/duplicates", "")
	wantStatus(t, w, http.StatusOK)
	got, _ := decodeData[dupPayload](t, w)
	if got.Pending {
		t.Error("a document without stored bytes can never be hashed, so it must not report pending")
	}
}

func TestMigration_RejectsUnknownDocType(t *testing.T) {
	withDB(t)
	doc := createDoc(t, uuid.New(), `{"name":"x.pdf"}`)
	err := models.DB.Model(&models.Document{}).Where("id = ?", doc.ID).UpdateColumn("doc_type", "passport").Error
	if err == nil {
		t.Error("chk_document_doc_type should reject an unknown doc_type")
	}
}

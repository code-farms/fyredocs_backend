//go:build integration

// Integration tests for document-service's handlers against the real schema:
// ownership scoping (scopeOwned), the two-phase delete lifecycle, folder/tag
// mutations, async export generation, delta-sync listing, and the migrations'
// constraints. These use Truncate isolation, never dbtest.Tx: the handlers
// commit, ListDocuments fans out onto two goroutines sharing models.DB, and
// generateExport runs on a detached goroutine — all three of the documented
// Tx exclusions (testing-strategy.md, "Database integration harness").
//
// NOTE: helpers here layer on the offline harness in harness_test.go
// (doAuthed, envelope, withUserService, fakeObjectStore) — same package, no
// duplication.
package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"fyredocs/shared/database/dbtest"
	"fyredocs/shared/response"

	"document-service/internal/migrations"
	"document-service/internal/models"
)

// withDB applies this service's migrations to the shared test database and
// swaps the package-global handle for the test's duration. The service name
// must stay "document-service" — it selects the goose history table production
// writes (internal/models/database.go Migrate).
func withDB(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := dbtest.Open(t, "document-service", migrations.FS)
	prev := models.DB
	models.DB = db
	t.Cleanup(func() { models.DB = prev })
	dbtest.Truncate(t, db,
		"documents", "folders", "tags", "document_tags", "exports", "job_workspace_hints")
}

// withNotificationStub keeps the detached export worker off the network:
// without it generateExport's best-effort Notify dials the compose hostname
// (notification-service:8091) and each export test eats the 4s client timeout.
func withNotificationStub(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("NOTIFICATION_SERVICE_URL", srv.URL)
}

// docPayload is the wire shape of a document in the response envelope; only
// the fields these tests assert on.
type docPayload struct {
	ID             uuid.UUID    `json:"id"`
	Name           string       `json:"name"`
	Status         string       `json:"status"`
	FolderID       *uuid.UUID   `json:"folderId"`
	OrganizationID *uuid.UUID   `json:"organizationId"`
	UpdatedAt      time.Time    `json:"updatedAt"`
	Tags           []tagPayload `json:"tags"`
}

// tagPayload is the wire shape of a tag.
type tagPayload struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// folderPayload is the wire shape of a folder.
type folderPayload struct {
	ID       uuid.UUID  `json:"id"`
	Name     string     `json:"name"`
	ParentID *uuid.UUID `json:"parentId"`
}

// exportPayload is the wire shape of an export (Content is json:"-", so bytes
// are asserted against the row, never the envelope).
type exportPayload struct {
	ID            uuid.UUID `json:"id"`
	Format        string    `json:"format"`
	Status        string    `json:"status"`
	FileName      string    `json:"fileName"`
	DocumentCount int       `json:"documentCount"`
}

// decodeData asserts a success envelope and returns its typed data + meta.
// Decoding into a concrete T (not map[string]any) is deliberate: a renamed
// JSON field then fails an assertion instead of silently zeroing.
func decodeData[T any](t *testing.T, w *httptest.ResponseRecorder) (T, *response.Meta) {
	t.Helper()
	var body struct {
		Success bool           `json:"success"`
		Data    T              `json:"data"`
		Meta    *response.Meta `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, w.Body.String())
	}
	if !body.Success {
		t.Fatalf("success = false: %s", w.Body.String())
	}
	return body.Data, body.Meta
}

// wantStatus fails fast with the body attached, since a wrong status makes
// every later assertion in the test meaningless.
func wantStatus(t *testing.T, w *httptest.ResponseRecorder, code int) {
	t.Helper()
	if w.Code != code {
		t.Fatalf("status = %d, want %d (body %s)", w.Code, code, w.Body.String())
	}
}

// seedDoc inserts a document row directly, bypassing the handler, so tests can
// arrange states the API cannot produce (foreign owner, custom timestamps).
func seedDoc(t *testing.T, uid uuid.UUID, mut func(*models.Document)) models.Document {
	t.Helper()
	d := models.Document{UserID: uid, Name: "seed-doc", Status: models.StatusUploaded}
	if mut != nil {
		mut(&d)
	}
	if err := models.DB.Create(&d).Error; err != nil {
		t.Fatalf("seed document: %v", err)
	}
	return d
}

// createDoc drives CreateDocument and returns the created payload.
func createDoc(t *testing.T, uid uuid.UUID, body string) docPayload {
	t.Helper()
	w := doAuthed(t, uid, http.MethodPost, "/documents", CreateDocument, "/documents", body)
	wantStatus(t, w, http.StatusCreated)
	doc, _ := decodeData[docPayload](t, w)
	return doc
}

// listDocs drives ListDocuments with a raw query string ("?..." or "").
func listDocs(t *testing.T, uid uuid.UUID, query string) ([]docPayload, *response.Meta) {
	t.Helper()
	w := doAuthed(t, uid, http.MethodGet, "/documents", ListDocuments, "/documents"+query, "")
	wantStatus(t, w, http.StatusOK)
	return decodeData[[]docPayload](t, w)
}

// docIDs flattens a listing to its id set for containment checks.
func docIDs(docs []docPayload) map[uuid.UUID]bool {
	ids := make(map[uuid.UUID]bool, len(docs))
	for _, d := range docs {
		ids[d.ID] = true
	}
	return ids
}

// reloadDoc fetches a row unscoped so assertions can see trashed/purged state.
func reloadDoc(t *testing.T, id uuid.UUID) (models.Document, bool) {
	t.Helper()
	var d models.Document
	err := models.DB.Unscoped().First(&d, "id = ?", id).Error
	if err != nil {
		if strings.Contains(err.Error(), "record not found") {
			return d, false
		}
		t.Fatalf("reload document %s: %v", id, err)
	}
	return d, true
}

// ─── Document CRUD lifecycle ───

func TestCreateDocument_PersistsOwnedRow(t *testing.T) {
	withDB(t)
	uid := uuid.New()

	created := createDoc(t, uid, `{"name":"  report.pdf  ","fileType":"pdf","mimeType":"application/pdf","fileSize":123}`)
	if created.Name != "report.pdf" {
		t.Errorf("name = %q, want trimmed %q", created.Name, "report.pdf")
	}
	if created.Status != models.StatusUploaded {
		t.Errorf("status = %q, want default %q", created.Status, models.StatusUploaded)
	}

	// Ownership must come from the verified identity, not the body — assert on
	// the ROW, since the payload omits nothing a handler could have faked.
	row, ok := reloadDoc(t, created.ID)
	if !ok {
		t.Fatal("created document row not found")
	}
	if row.UserID != uid {
		t.Errorf("row.UserID = %s, want caller %s", row.UserID, uid)
	}
	if row.OrganizationID != nil {
		t.Errorf("row.OrganizationID = %v, want NULL for personal scope", row.OrganizationID)
	}

	w := doAuthed(t, uid, http.MethodGet, "/documents/:id", GetDocument, "/documents/"+created.ID.String(), "")
	wantStatus(t, w, http.StatusOK)
	got, _ := decodeData[docPayload](t, w)
	if got.ID != created.ID || got.Name != "report.pdf" {
		t.Errorf("get returned (%s, %q), want (%s, %q)", got.ID, got.Name, created.ID, "report.pdf")
	}
}

func TestUpdateDocument_RenameMoveRestatus(t *testing.T) {
	withDB(t)
	uid := uuid.New()
	doc := seedDoc(t, uid, nil)

	fw := doAuthed(t, uid, http.MethodPost, "/folders", CreateFolder, "/folders", `{"name":"projects"}`)
	wantStatus(t, fw, http.StatusCreated)
	folder, _ := decodeData[folderPayload](t, fw)

	body := fmt.Sprintf(`{"name":"renamed.pdf","folderId":"%s","status":"ready"}`, folder.ID)
	w := doAuthed(t, uid, http.MethodPatch, "/documents/:id", UpdateDocument, "/documents/"+doc.ID.String(), body)
	wantStatus(t, w, http.StatusOK)

	row, _ := reloadDoc(t, doc.ID)
	if row.Name != "renamed.pdf" || row.Status != models.StatusReady {
		t.Errorf("row (name, status) = (%q, %q), want (renamed.pdf, ready)", row.Name, row.Status)
	}
	if row.FolderID == nil || *row.FolderID != folder.ID {
		t.Errorf("row.FolderID = %v, want %s", row.FolderID, folder.ID)
	}

	// Pins a quirk: UpdateDocument's own code treats folderId:"" as "move to
	// root" (parseUUID maps "" to nil), but the binding tag
	// `omitempty,uuid` rejects a non-nil empty string BEFORE the handler
	// runs — so the move-to-root branch is unreachable over HTTP and clients
	// get 400. Fixing the tag (or validating in the handler) flips this to
	// expecting 200 + NULL folder_id.
	w = doAuthed(t, uid, http.MethodPatch, "/documents/:id", UpdateDocument, "/documents/"+doc.ID.String(), `{"folderId":""}`)
	wantErrEnvelope(t, w, http.StatusBadRequest, response.CodeInvalidInput)
	row, _ = reloadDoc(t, doc.ID)
	if row.FolderID == nil || *row.FolderID != folder.ID {
		t.Errorf("row.FolderID = %v, want unchanged %s (rejected request must not mutate)", row.FolderID, folder.ID)
	}
}

func TestDocumentTrashLifecycle(t *testing.T) {
	withDB(t)
	uid := uuid.New()
	doc := seedDoc(t, uid, nil)
	keep := seedDoc(t, uid, func(d *models.Document) { d.Name = "keeper" })

	w := doAuthed(t, uid, http.MethodDelete, "/documents/:id", DeleteDocument, "/documents/"+doc.ID.String(), "")
	wantStatus(t, w, http.StatusOK)

	// Soft delete: gone from the library, present in Trash, row still exists.
	docs, meta := listDocs(t, uid, "")
	if ids := docIDs(docs); ids[doc.ID] || !ids[keep.ID] {
		t.Errorf("library listing after delete = %v, want only %s", ids, keep.ID)
	}
	if meta == nil || meta.Total != 1 {
		t.Errorf("meta.Total = %v, want 1", meta)
	}
	trash, _ := listDocs(t, uid, "?trashed=true")
	if ids := docIDs(trash); !ids[doc.ID] || ids[keep.ID] {
		t.Errorf("trash listing = %v, want only %s", ids, doc.ID)
	}
	if row, ok := reloadDoc(t, doc.ID); !ok || !row.DeletedAt.Valid {
		t.Fatalf("trashed row (found=%v, deleted_at valid=%v), want soft-deleted row", ok, row.DeletedAt.Valid)
	}

	// Restoring a live document must 404: RowsAffected==0 on the
	// deleted_at IS NOT NULL predicate, so restore cannot touch live rows.
	w = doAuthed(t, uid, http.MethodPost, "/documents/:id/restore", RestoreDocument, "/documents/"+keep.ID.String()+"/restore", "")
	wantErrEnvelope(t, w, http.StatusNotFound, response.CodeNotFound)

	w = doAuthed(t, uid, http.MethodPost, "/documents/:id/restore", RestoreDocument, "/documents/"+doc.ID.String()+"/restore", "")
	wantStatus(t, w, http.StatusOK)
	docs, _ = listDocs(t, uid, "")
	if ids := docIDs(docs); !ids[doc.ID] {
		t.Errorf("restored document %s missing from library listing", doc.ID)
	}
}

func TestPurgeDocument_BackupObjects(t *testing.T) {
	// Purge removes stored BYTES only for backups/ keys: job-output keys stay
	// owned by job-service's lifecycle, and nothing else sweeps backups/.
	tests := []struct {
		name        string
		storagePath func(uid uuid.UUID) string
		removeErr   error
		wantRemoved bool
	}{
		{
			name:        "backup key removed from store",
			storagePath: func(uid uuid.UUID) string { return "backups/" + uid.String() + "/b.pdf" },
			wantRemoved: true,
		},
		{
			name:        "non-backup key never touches the store",
			storagePath: func(uuid.UUID) string { return "outputs/job-123/out.pdf" },
			wantRemoved: false,
		},
		{
			name:        "row is purged even when the store errors",
			storagePath: func(uid uuid.UUID) string { return "backups/" + uid.String() + "/b.pdf" },
			removeErr:   fmt.Errorf("minio down"),
			wantRemoved: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withDB(t)
			fs := withFakeObjectStore(t)
			fs.removeErr = tc.removeErr
			uid := uuid.New()
			doc := seedDoc(t, uid, func(d *models.Document) { d.StoragePath = tc.storagePath(uid) })

			w := doAuthed(t, uid, http.MethodDelete, "/documents/:id/permanent", PurgeDocument,
				"/documents/"+doc.ID.String()+"/permanent", "")
			wantStatus(t, w, http.StatusOK)

			// The row is the source of truth: it must be hard-gone regardless
			// of what the object store did (a leaked object is only logged).
			if _, ok := reloadDoc(t, doc.ID); ok {
				t.Error("document row still present after purge")
			}
			if tc.wantRemoved {
				want := "test-outputs/" + tc.storagePath(uid)
				if len(fs.removed) != 1 || fs.removed[0] != want {
					t.Errorf("removed = %v, want [%s]", fs.removed, want)
				}
			} else if len(fs.removed) != 0 {
				t.Errorf("removed = %v, want no store calls", fs.removed)
			}
		})
	}
}

func TestPurgeDocument_TaggedRowPurges(t *testing.T) {
	// Regression guard: document_tags carries an FK to documents, and
	// PurgeDocument once deleted the parent row BEFORE its association rows —
	// fk_document_tags_document rejected that, so every tagged document was
	// permanently unpurgeable (500). Associations now go first.
	withDB(t)
	withFakeObjectStore(t)
	uid := uuid.New()
	doc := seedDoc(t, uid, nil)
	tag := models.Tag{UserID: uid, Name: "blocked"}
	if err := models.DB.Create(&tag).Error; err != nil {
		t.Fatalf("seed tag: %v", err)
	}
	if err := models.DB.Exec("INSERT INTO document_tags (document_id, tag_id) VALUES (?, ?)", doc.ID, tag.ID).Error; err != nil {
		t.Fatalf("seed association: %v", err)
	}

	w := doAuthed(t, uid, http.MethodDelete, "/documents/:id/permanent", PurgeDocument,
		"/documents/"+doc.ID.String()+"/permanent", "")
	wantStatus(t, w, http.StatusOK)
	if _, ok := reloadDoc(t, doc.ID); ok {
		t.Error("tagged document row survived its purge")
	}
	var n int64
	models.DB.Raw("SELECT count(*) FROM document_tags WHERE document_id = ?", doc.ID).Scan(&n)
	if n != 0 {
		t.Errorf("association rows survived the purge: %d", n)
	}
}

// ─── Ownership isolation (IDOR) ───

func TestOwnership_CrossUserIsolation(t *testing.T) {
	withDB(t)
	fs := withFakeObjectStore(t)
	owner, intruder := uuid.New(), uuid.New()
	doc := seedDoc(t, owner, func(d *models.Document) {
		d.Name = "classified.pdf"
		d.StoragePath = "outputs/job-1/classified.pdf"
	})
	id := doc.ID.String()

	// Every per-document verb must answer 404 (not 403) for a foreign row:
	// scopeOwned makes other users' documents nonexistent, not forbidden,
	// so ids cannot be probed for existence.
	tests := []struct {
		name    string
		method  string
		pattern string
		h       gin.HandlerFunc
		target  string
		body    string
	}{
		{"get", http.MethodGet, "/documents/:id", GetDocument, "/documents/" + id, ""},
		{"patch", http.MethodPatch, "/documents/:id", UpdateDocument, "/documents/" + id, `{"name":"stolen"}`},
		{"soft delete", http.MethodDelete, "/documents/:id", DeleteDocument, "/documents/" + id, ""},
		{"restore", http.MethodPost, "/documents/:id/restore", RestoreDocument, "/documents/" + id + "/restore", ""},
		{"purge", http.MethodDelete, "/documents/:id/permanent", PurgeDocument, "/documents/" + id + "/permanent", ""},
		{"download", http.MethodGet, "/documents/:id/download", DownloadDocument, "/documents/" + id + "/download", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := doAuthed(t, intruder, tc.method, tc.pattern, tc.h, tc.target, tc.body)
			wantErrEnvelope(t, w, http.StatusNotFound, response.CodeNotFound)
		})
	}

	// The row must be byte-for-byte untouched and no object deleted — a 404
	// that still mutated state would be the worse bug.
	row, ok := reloadDoc(t, doc.ID)
	if !ok || row.Name != "classified.pdf" || row.DeletedAt.Valid {
		t.Errorf("row after intruder attempts = (found=%v, name=%q, trashed=%v), want untouched", ok, row.Name, row.DeletedAt.Valid)
	}
	if len(fs.removed) != 0 {
		t.Errorf("object store saw removals from an intruder: %v", fs.removed)
	}

	// Positive control: the owner still reaches the document, proving the
	// 404s above came from scoping and not a broken fixture.
	w := doAuthed(t, owner, http.MethodGet, "/documents/:id", GetDocument, "/documents/"+id, "")
	wantStatus(t, w, http.StatusOK)
}

func TestOrgScope_MemberVisibilityMatrix(t *testing.T) {
	withDB(t)
	creator, member := uuid.New(), uuid.New()
	orgID := uuid.New()

	withUserService(t, membershipRole("editor"))
	created := createDoc(t, creator, fmt.Sprintf(`{"name":"org-doc","organizationId":"%s"}`, orgID))
	row, _ := reloadDoc(t, created.ID)
	if row.OrganizationID == nil || *row.OrganizationID != orgID {
		t.Fatalf("row.OrganizationID = %v, want %s", row.OrganizationID, orgID)
	}
	target := "/documents/" + created.ID.String()

	t.Run("same-org member sees it with org scope", func(t *testing.T) {
		withUserService(t, membershipRole("viewer"))
		w := doAuthed(t, member, http.MethodGet, "/documents/:id", GetDocument, target+"?orgId="+orgID.String(), "")
		wantStatus(t, w, http.StatusOK)
	})
	t.Run("viewer role cannot write", func(t *testing.T) {
		withUserService(t, membershipRole("viewer"))
		w := doAuthed(t, member, http.MethodPatch, "/documents/:id", UpdateDocument, target+"?orgId="+orgID.String(), `{"name":"x"}`)
		wantErrEnvelope(t, w, http.StatusForbidden, response.CodeForbidden)
	})
	t.Run("invisible without org scope", func(t *testing.T) {
		// Even the creator: personal scope is organization_id IS NULL, so an
		// org document is out of reach without naming the org.
		noUserService(t)
		for _, uid := range []uuid.UUID{member, creator} {
			w := doAuthed(t, uid, http.MethodGet, "/documents/:id", GetDocument, target, "")
			wantErrEnvelope(t, w, http.StatusNotFound, response.CodeNotFound)
		}
	})
	t.Run("non-member gets 404 from membership check", func(t *testing.T) {
		withUserService(t, membershipStatus(http.StatusNotFound))
		w := doAuthed(t, member, http.MethodGet, "/documents/:id", GetDocument, target+"?orgId="+orgID.String(), "")
		wantErrEnvelope(t, w, http.StatusNotFound, response.CodeNotFound)
	})
}

func TestOrgScope_PersonalDocsHiddenFromOrgMembers(t *testing.T) {
	withDB(t)
	alice, bob := uuid.New(), uuid.New()
	orgID := uuid.New()
	personal := seedDoc(t, alice, func(d *models.Document) { d.Name = "alices-private.pdf" })

	// Bob is a member of Alice's org, but membership grants the ORG's rows
	// only: personal rows have organization_id NULL and never match org scope.
	withUserService(t, membershipRole("admin"))
	docs, _ := listDocs(t, bob, "?orgId="+orgID.String())
	if len(docs) != 0 {
		t.Errorf("org listing = %d docs, want 0 (personal docs leaked into org scope)", len(docs))
	}
	w := doAuthed(t, bob, http.MethodGet, "/documents/:id", GetDocument,
		"/documents/"+personal.ID.String()+"?orgId="+orgID.String(), "")
	wantErrEnvelope(t, w, http.StatusNotFound, response.CodeNotFound)
}

func TestUpdateDocument_MoveToOrgScope(t *testing.T) {
	withDB(t)
	uid := uuid.New()
	orgID := uuid.New()
	doc := seedDoc(t, uid, nil)

	withUserService(t, membershipRole("editor"))
	w := doAuthed(t, uid, http.MethodPatch, "/documents/:id", UpdateDocument,
		"/documents/"+doc.ID.String(), fmt.Sprintf(`{"organizationId":"%s"}`, orgID))
	wantStatus(t, w, http.StatusOK)

	row, _ := reloadDoc(t, doc.ID)
	if row.OrganizationID == nil || *row.OrganizationID != orgID {
		t.Fatalf("row.OrganizationID = %v, want %s after scope move", row.OrganizationID, orgID)
	}

	// Pins a quirk: UpdateDocument reloads the response under the REQUEST's
	// scope (personal here), which the row just left, so the reload misses and
	// the 200 carries a zero-value document. Clients must not trust this
	// payload after a scope move; fixing the reload flips this assertion.
	got, _ := decodeData[docPayload](t, w)
	if got.ID != uuid.Nil {
		t.Errorf("response doc id = %s, want zero-value payload (reload-under-old-scope quirk changed; update this pin)", got.ID)
	}
}

// ─── Download ───

func TestDownloadDocument_PresignRedirect(t *testing.T) {
	withDB(t)
	withFakeObjectStore(t)
	uid := uuid.New()
	doc := seedDoc(t, uid, func(d *models.Document) {
		d.Name = "report"
		d.FileType = "pdf"
		d.StoragePath = "outputs/job-1/report.pdf"
	})

	w := doAuthed(t, uid, http.MethodGet, "/documents/:id/download", DownloadDocument,
		"/documents/"+doc.ID.String()+"/download", "")
	wantStatus(t, w, http.StatusFound)
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "http://minio.test/test-outputs/outputs/job-1/report.pdf?") {
		t.Errorf("Location = %q, want fake presign of the row's storage key", loc)
	}
	// The attachment filename gains the row's fileType extension so the saved
	// file opens correctly even when the stored name has none.
	if !strings.Contains(loc, url.QueryEscape(`attachment; filename="report.pdf"`)) {
		t.Errorf("Location %q missing content-disposition for report.pdf", loc)
	}
}

func TestDownloadDocument_NoStoredFile404(t *testing.T) {
	withDB(t)
	withFakeObjectStore(t)
	uid := uuid.New()
	// A job-era row: metadata only, bytes live behind the job download route.
	doc := seedDoc(t, uid, func(d *models.Document) { d.StoragePath = "" })

	w := doAuthed(t, uid, http.MethodGet, "/documents/:id/download", DownloadDocument,
		"/documents/"+doc.ID.String()+"/download", "")
	wantErrEnvelope(t, w, http.StatusNotFound, response.CodeNotFound)
}

// ─── Folders ───

func TestFolderLifecycle_NestMoveDelete(t *testing.T) {
	withDB(t)
	uid := uuid.New()

	w := doAuthed(t, uid, http.MethodPost, "/folders", CreateFolder, "/folders", `{"name":"parent"}`)
	wantStatus(t, w, http.StatusCreated)
	parent, _ := decodeData[folderPayload](t, w)

	w = doAuthed(t, uid, http.MethodPost, "/folders", CreateFolder, "/folders",
		fmt.Sprintf(`{"name":"child","parentId":"%s"}`, parent.ID))
	wantStatus(t, w, http.StatusCreated)
	child, _ := decodeData[folderPayload](t, w)
	if child.ParentID == nil || *child.ParentID != parent.ID {
		t.Fatalf("child.ParentID = %v, want %s", child.ParentID, parent.ID)
	}

	w = doAuthed(t, uid, http.MethodGet, "/folders", ListFolders, "/folders?parentId="+parent.ID.String(), "")
	wantStatus(t, w, http.StatusOK)
	subs, _ := decodeData[[]folderPayload](t, w)
	if len(subs) != 1 || subs[0].ID != child.ID {
		t.Fatalf("children of parent = %v, want [%s]", subs, child.ID)
	}

	doc := seedDoc(t, uid, func(d *models.Document) { fid := child.ID; d.FolderID = &fid })

	w = doAuthed(t, uid, http.MethodDelete, "/folders/:id", DeleteFolder, "/folders/"+child.ID.String(), "")
	wantStatus(t, w, http.StatusOK)

	// Deleting a folder must not trap its documents: they move to root, while
	// the folder itself is only soft-deleted (restorable data, hidden lists).
	row, _ := reloadDoc(t, doc.ID)
	if row.FolderID != nil {
		t.Errorf("document.FolderID = %v after folder delete, want NULL (root)", row.FolderID)
	}
	var folderRow models.Folder
	if err := models.DB.Unscoped().First(&folderRow, "id = ?", child.ID).Error; err != nil {
		t.Fatalf("reload folder: %v", err)
	}
	if !folderRow.DeletedAt.Valid {
		t.Error("folder row not soft-deleted")
	}
	w = doAuthed(t, uid, http.MethodGet, "/folders", ListFolders, "/folders", "")
	wantStatus(t, w, http.StatusOK)
	remaining, _ := decodeData[[]folderPayload](t, w)
	if len(remaining) != 1 || remaining[0].ID != parent.ID {
		t.Errorf("folders after delete = %v, want only parent %s", remaining, parent.ID)
	}
}

func TestDeleteFolder_ChildFoldersKeepDanglingParent(t *testing.T) {
	// Pins current behavior: DeleteFolder reparents DOCUMENTS to root but does
	// not touch child FOLDERS, so a child keeps parent_id aimed at a
	// soft-deleted folder and vanishes from parentId-filtered listings while
	// staying in the flat list. Worth knowing before anyone builds a tree UI.
	withDB(t)
	uid := uuid.New()
	parent := models.Folder{UserID: uid, Name: "doomed"}
	if err := models.DB.Create(&parent).Error; err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	pid := parent.ID
	child := models.Folder{UserID: uid, Name: "orphan", ParentID: &pid}
	if err := models.DB.Create(&child).Error; err != nil {
		t.Fatalf("seed child: %v", err)
	}

	w := doAuthed(t, uid, http.MethodDelete, "/folders/:id", DeleteFolder, "/folders/"+parent.ID.String(), "")
	wantStatus(t, w, http.StatusOK)

	var got models.Folder
	if err := models.DB.First(&got, "id = ?", child.ID).Error; err != nil {
		t.Fatalf("child folder gone: %v", err)
	}
	if got.ParentID == nil || *got.ParentID != parent.ID {
		t.Errorf("child.ParentID = %v, want dangling %s (behavior changed; update this pin)", got.ParentID, parent.ID)
	}
}

// ─── Tags ───

func TestTagAttachDetachLifecycle(t *testing.T) {
	withDB(t)
	uid := uuid.New()
	doc := seedDoc(t, uid, nil)

	w := doAuthed(t, uid, http.MethodPost, "/tags", CreateTag, "/tags", `{"name":"invoices","color":"#ff0000"}`)
	wantStatus(t, w, http.StatusCreated)
	tag, _ := decodeData[tagPayload](t, w)

	w = doAuthed(t, uid, http.MethodPost, "/documents/:id/tags", AttachTag,
		"/documents/"+doc.ID.String()+"/tags", fmt.Sprintf(`{"tagId":"%s"}`, tag.ID))
	wantStatus(t, w, http.StatusOK)

	// The attachment must be visible through both read paths: the tagId list
	// filter (JOIN on document_tags) and the document's preloaded tags.
	docs, _ := listDocs(t, uid, "?tagId="+tag.ID.String())
	if !docIDs(docs)[doc.ID] {
		t.Errorf("tagId filter missed the tagged document")
	}
	w = doAuthed(t, uid, http.MethodGet, "/documents/:id", GetDocument, "/documents/"+doc.ID.String(), "")
	wantStatus(t, w, http.StatusOK)
	got, _ := decodeData[docPayload](t, w)
	if len(got.Tags) != 1 || got.Tags[0].Name != "invoices" {
		t.Errorf("preloaded tags = %v, want [invoices]", got.Tags)
	}

	w = doAuthed(t, uid, http.MethodDelete, "/documents/:id/tags/:tagId", DetachTag,
		"/documents/"+doc.ID.String()+"/tags/"+tag.ID.String(), "")
	wantStatus(t, w, http.StatusOK)
	docs, _ = listDocs(t, uid, "?tagId="+tag.ID.String())
	if len(docs) != 0 {
		t.Errorf("tagId filter still returns %d docs after detach", len(docs))
	}
	var links int64
	if err := models.DB.Table("document_tags").Count(&links).Error; err != nil {
		t.Fatalf("count document_tags: %v", err)
	}
	if links != 0 {
		t.Errorf("document_tags rows = %d after detach, want 0", links)
	}
}

func TestDeleteTag_AssociationOrderingFK(t *testing.T) {
	withDB(t)
	uid := uuid.New()

	t.Run("unattached tag deletes cleanly", func(t *testing.T) {
		tag := models.Tag{UserID: uid, Name: "loose"}
		if err := models.DB.Create(&tag).Error; err != nil {
			t.Fatalf("seed tag: %v", err)
		}
		w := doAuthed(t, uid, http.MethodDelete, "/tags/:id", DeleteTag, "/tags/"+tag.ID.String(), "")
		wantStatus(t, w, http.StatusOK)
		var n int64
		models.DB.Model(&models.Tag{}).Where("id = ?", tag.ID).Count(&n)
		if n != 0 {
			t.Error("tag row survived delete")
		}
	})

	t.Run("attached tag deletes with its associations", func(t *testing.T) {
		// Regression guard for the same ordering defect PurgeDocument had:
		// DeleteTag once removed the tag row BEFORE its document_tags rows,
		// and fk_document_tags_tag rejected that — every in-use tag was
		// undeletable (500). Associations now go first, after a scoped fetch
		// proves ownership.
		doc := seedDoc(t, uid, nil)
		tag := models.Tag{UserID: uid, Name: "in-use"}
		if err := models.DB.Create(&tag).Error; err != nil {
			t.Fatalf("seed tag: %v", err)
		}
		if err := models.DB.Exec("INSERT INTO document_tags (document_id, tag_id) VALUES (?, ?)", doc.ID, tag.ID).Error; err != nil {
			t.Fatalf("seed association: %v", err)
		}
		w := doAuthed(t, uid, http.MethodDelete, "/tags/:id", DeleteTag, "/tags/"+tag.ID.String(), "")
		wantStatus(t, w, http.StatusOK)
		var n int64
		models.DB.Model(&models.Tag{}).Where("id = ?", tag.ID).Count(&n)
		if n != 0 {
			t.Error("in-use tag row survived its delete")
		}
		models.DB.Raw("SELECT count(*) FROM document_tags WHERE tag_id = ?", tag.ID).Scan(&n)
		if n != 0 {
			t.Errorf("association rows survived the tag delete: %d", n)
		}
	})
}

func TestCreateTag_DuplicateNameIdempotent(t *testing.T) {
	withDB(t)
	uid := uuid.New()

	w := doAuthed(t, uid, http.MethodPost, "/tags", CreateTag, "/tags", `{"name":"projects"}`)
	wantStatus(t, w, http.StatusCreated)
	first, _ := decodeData[tagPayload](t, w)

	// Re-creating the same name must not error and must not mint a second
	// row: the handler answers 200 with the EXISTING tag, so clients can
	// treat create as ensure.
	w = doAuthed(t, uid, http.MethodPost, "/tags", CreateTag, "/tags", `{"name":"projects"}`)
	wantStatus(t, w, http.StatusOK)
	again, _ := decodeData[tagPayload](t, w)
	if again.ID != first.ID {
		t.Errorf("duplicate create returned id %s, want existing %s", again.ID, first.ID)
	}
	var n int64
	models.DB.Model(&models.Tag{}).Where("user_id = ?", uid).Count(&n)
	if n != 1 {
		t.Errorf("tag rows = %d, want 1", n)
	}
}

// ─── Exports ───

func TestExportLifecycle_AsyncCSV(t *testing.T) {
	withDB(t)
	withNotificationStub(t)
	uid, other := uuid.New(), uuid.New()
	seedDoc(t, uid, func(d *models.Document) { d.Name = "alpha-report" })
	seedDoc(t, uid, func(d *models.Document) { d.Name = "beta-notes" })
	seedDoc(t, other, func(d *models.Document) { d.Name = "intruder-doc" })

	// Uppercase format pins the case-insensitive contract.
	w := doAuthed(t, uid, http.MethodPost, "/exports", CreateExport, "/exports", `{"format":"CSV"}`)
	wantStatus(t, w, http.StatusCreated)
	created, _ := decodeData[exportPayload](t, w)
	if created.Format != "csv" || created.Status != models.ExportQueued {
		t.Fatalf("created export (format, status) = (%q, %q), want (csv, queued)", created.Format, created.Status)
	}

	// generateExport runs on a detached goroutine behind the semaphore; the
	// deterministic observable is the row reaching a terminal status.
	exp := waitExportDone(t, created.ID)
	if exp.Status != models.ExportReady {
		t.Fatalf("export status = %q (error %q), want ready", exp.Status, exp.Error)
	}
	if exp.DocumentCount != 2 {
		t.Errorf("documentCount = %d, want 2 (other users' docs must not export)", exp.DocumentCount)
	}
	csvBody := string(exp.Content)
	for _, name := range []string{"Name,Type,Size (bytes),Status,Created,Tags", "alpha-report", "beta-notes"} {
		if !strings.Contains(csvBody, name) {
			t.Errorf("CSV missing %q:\n%s", name, csvBody)
		}
	}
	if strings.Contains(csvBody, "intruder-doc") {
		t.Errorf("CSV leaked another user's document:\n%s", csvBody)
	}

	w = doAuthed(t, uid, http.MethodGet, "/exports/:id/download", DownloadExport,
		"/exports/"+created.ID.String()+"/download", "")
	wantStatus(t, w, http.StatusOK)
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type = %q, want text/csv", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, ".csv") {
		t.Errorf("Content-Disposition = %q, want a .csv attachment", cd)
	}
	if got := w.Body.String(); got != csvBody {
		t.Errorf("download bytes differ from the stored artifact")
	}
}

func TestGenerateExport_SynchronousScopedAndFiltered(t *testing.T) {
	// Calls generateExport directly (same package) so the semaphore/status
	// machine is exercised without polling: everything after this call is a
	// committed row.
	withDB(t)
	withNotificationStub(t)
	uid, other := uuid.New(), uuid.New()
	seedDoc(t, uid, func(d *models.Document) { d.Name = "ready-one"; d.Status = models.StatusReady })
	seedDoc(t, uid, func(d *models.Document) { d.Name = "still-uploaded" })
	seedDoc(t, other, func(d *models.Document) { d.Name = "foreign"; d.Status = models.StatusReady })

	filters, _ := json.Marshal(exportFilters{Status: models.StatusReady})
	exp := models.Export{UserID: uid, Format: "csv", Status: models.ExportQueued, Filters: filters}
	if err := models.DB.Create(&exp).Error; err != nil {
		t.Fatalf("seed export: %v", err)
	}

	generateExport(exp.ID)

	var got models.Export
	if err := models.DB.First(&got, "id = ?", exp.ID).Error; err != nil {
		t.Fatalf("reload export: %v", err)
	}
	if got.Status != models.ExportReady || got.CompletedAt == nil {
		t.Fatalf("export (status, completedAt) = (%q, %v), want (ready, set); error %q", got.Status, got.CompletedAt, got.Error)
	}
	if got.DocumentCount != 1 {
		t.Errorf("documentCount = %d, want 1 (status filter + owner scope)", got.DocumentCount)
	}
	body := string(got.Content)
	if !strings.Contains(body, "ready-one") {
		t.Errorf("CSV missing the filtered-in document:\n%s", body)
	}
	for _, leaked := range []string{"still-uploaded", "foreign"} {
		if strings.Contains(body, leaked) {
			t.Errorf("CSV contains %q, which the filter/scope must exclude:\n%s", leaked, body)
		}
	}
	if !strings.HasPrefix(got.FileName, "documents-") || !strings.HasSuffix(got.FileName, ".csv") {
		t.Errorf("fileName = %q, want documents-<ts>.csv", got.FileName)
	}
}

func TestExportGuards_OwnershipAndReadiness(t *testing.T) {
	withDB(t)
	owner, intruder := uuid.New(), uuid.New()
	exp := models.Export{UserID: owner, Format: "csv", Status: models.ExportQueued}
	if err := models.DB.Create(&exp).Error; err != nil {
		t.Fatalf("seed export: %v", err)
	}
	id := exp.ID.String()

	// A queued export must 409, not stream an empty artifact.
	w := doAuthed(t, owner, http.MethodGet, "/exports/:id/download", DownloadExport, "/exports/"+id+"/download", "")
	wantErrEnvelope(t, w, http.StatusConflict, "NOT_READY")

	// Foreign exports are nonexistent (404), matching document scoping.
	w = doAuthed(t, intruder, http.MethodGet, "/exports/:id", GetExport, "/exports/"+id, "")
	wantErrEnvelope(t, w, http.StatusNotFound, response.CodeNotFound)
	w = doAuthed(t, intruder, http.MethodGet, "/exports/:id/download", DownloadExport, "/exports/"+id+"/download", "")
	wantErrEnvelope(t, w, http.StatusNotFound, response.CodeNotFound)

	w = doAuthed(t, owner, http.MethodGet, "/exports", ListExports, "/exports", "")
	wantStatus(t, w, http.StatusOK)
	mine, _ := decodeData[[]exportPayload](t, w)
	if len(mine) != 1 || mine[0].ID != exp.ID {
		t.Errorf("owner's export listing = %v, want [%s]", mine, exp.ID)
	}
	w = doAuthed(t, intruder, http.MethodGet, "/exports", ListExports, "/exports", "")
	wantStatus(t, w, http.StatusOK)
	theirs, _ := decodeData[[]exportPayload](t, w)
	if len(theirs) != 0 {
		t.Errorf("intruder's export listing = %v, want empty", theirs)
	}
}

// waitExportDone polls the export row until a terminal status. Polling the ROW
// (not sleeping a fixed interval) keeps the async test deterministic under
// load: the detached worker owns the transition, the test only observes it.
func waitExportDone(t *testing.T, id uuid.UUID) models.Export {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var exp models.Export
		if err := models.DB.First(&exp, "id = ?", id).Error; err != nil {
			t.Fatalf("load export %s: %v", id, err)
		}
		if exp.Status == models.ExportReady || exp.Status == models.ExportFailed {
			return exp
		}
		if time.Now().After(deadline) {
			t.Fatalf("export %s never left status %q", id, exp.Status)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// ─── Delta-sync listing ───

func TestListDocuments_UpdatedSinceAndSortUpdated(t *testing.T) {
	withDB(t)
	uid := uuid.New()
	oldest := seedDoc(t, uid, func(d *models.Document) { d.Name = "oldest" })
	mid := seedDoc(t, uid, func(d *models.Document) { d.Name = "mid" })
	newest := seedDoc(t, uid, func(d *models.Document) { d.Name = "newest" })

	// Raw SQL, not GORM updates: GORM would stamp updated_at back to now() and
	// erase exactly the spread this test needs.
	base := time.Now().UTC().Truncate(time.Second)
	for id, ts := range map[uuid.UUID]time.Time{
		oldest.ID: base.Add(-3 * time.Hour),
		mid.ID:    base.Add(-2 * time.Hour),
		newest.ID: base.Add(-1 * time.Hour),
	} {
		if err := models.DB.Exec("UPDATE documents SET updated_at = ? WHERE id = ?", ts, id).Error; err != nil {
			t.Fatalf("backdate %s: %v", id, err)
		}
	}

	// updatedSince is strictly exclusive: a client syncing from its last-seen
	// timestamp must not re-download the row that produced that timestamp.
	since := base.Add(-3 * time.Hour)
	query := "?updatedSince=" + url.QueryEscape(since.Format(time.RFC3339)) + "&sort=updated"
	docs, meta := listDocs(t, uid, query)
	if len(docs) != 2 {
		t.Fatalf("delta listing = %d docs, want 2 (row at the boundary excluded)", len(docs))
	}
	if docs[0].ID != newest.ID || docs[1].ID != mid.ID {
		t.Errorf("order = [%s %s], want updated_at DESC [%s %s]", docs[0].Name, docs[1].Name, "newest", "mid")
	}
	if meta == nil || meta.Total != 2 {
		t.Errorf("meta.Total = %v, want 2 (count must apply the same filter)", meta)
	}
}

// ─── Schema constraints (internal/migrations) ───

func TestSchemaConstraints(t *testing.T) {
	withDB(t)
	uid := uuid.New()

	t.Run("documents.status CHECK rejects unknown values", func(t *testing.T) {
		// 00002_enum_check_constraints.sql: a bad write must fail loudly at
		// the DB rather than minting a row no code path can handle.
		err := models.DB.Exec(
			"INSERT INTO documents (id, user_id, name, status) VALUES (?, ?, ?, ?)",
			uuid.New(), uid, "bad-status", "archived").Error
		if err == nil || !strings.Contains(err.Error(), "chk_document_status") {
			t.Errorf("insert err = %v, want chk_document_status violation", err)
		}
	})

	t.Run("one document per (user, source job)", func(t *testing.T) {
		// idx_doc_user_source_job is what makes job finalize idempotent: a
		// retried finalize must collide, not duplicate the document.
		jid := uuid.New()
		first := models.Document{UserID: uid, Name: "from-job", SourceJobID: &jid}
		if err := models.DB.Create(&first).Error; err != nil {
			t.Fatalf("first job document: %v", err)
		}
		dup := models.Document{UserID: uid, Name: "from-job-again", SourceJobID: &jid}
		if err := models.DB.Create(&dup).Error; err == nil || !strings.Contains(err.Error(), "idx_doc_user_source_job") {
			t.Errorf("duplicate insert err = %v, want idx_doc_user_source_job violation", err)
		}
		// The index is per-user: another user materializing the same job id is
		// a different document, not a collision.
		other := models.Document{UserID: uuid.New(), Name: "other-owner", SourceJobID: &jid}
		if err := models.DB.Create(&other).Error; err != nil {
			t.Errorf("same job id under another user: %v, want success", err)
		}
	})

	t.Run("personal tag names unique per user", func(t *testing.T) {
		if err := models.DB.Create(&models.Tag{UserID: uid, Name: "unique-me"}).Error; err != nil {
			t.Fatalf("first tag: %v", err)
		}
		err := models.DB.Create(&models.Tag{UserID: uid, Name: "unique-me"}).Error
		if err == nil || !strings.Contains(err.Error(), "idx_tag_personal") {
			t.Errorf("duplicate personal tag err = %v, want idx_tag_personal violation", err)
		}
		// The partial index is scoped by user: another user reusing the name
		// is fine.
		if err := models.DB.Create(&models.Tag{UserID: uuid.New(), Name: "unique-me"}).Error; err != nil {
			t.Errorf("same name under another user: %v, want success", err)
		}
	})

	t.Run("org tag names unique per org across users", func(t *testing.T) {
		// idx_tag_org keys on (organization_id, name) with no user column: two
		// MEMBERS creating the same org tag collide — the tag namespace
		// belongs to the org, not to whoever typed it first.
		orgID := uuid.New()
		if err := models.DB.Create(&models.Tag{UserID: uid, OrganizationID: &orgID, Name: "shared"}).Error; err != nil {
			t.Fatalf("first org tag: %v", err)
		}
		err := models.DB.Create(&models.Tag{UserID: uuid.New(), OrganizationID: &orgID, Name: "shared"}).Error
		if err == nil || !strings.Contains(err.Error(), "idx_tag_org") {
			t.Errorf("duplicate org tag err = %v, want idx_tag_org violation", err)
		}
	})
}

// Tests for document handlers' pure gates and pre-DB guard paths. The
// invariants under guard: the backups/ storage prefix is an ownership
// assertion — a caller can never register another user's backup object as
// their own document — and every malformed identifier or body is rejected
// before a single query runs (models.DB is nil here; see harness_test.go).
package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"fyredocs/shared/authverify"
	"fyredocs/shared/response"
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
		// Regression rows: a ".." segment under an owned prefix used to pass
		// the prefix check and could re-resolve to another user's key at any
		// dot-segment-normalizing hop.
		{"dot-dot traversal under own prefix rejected", "backups/" + me.String() + "/../" + other.String() + "/f.pdf", false},
		{"trailing dot-dot segment rejected", "backups/" + me.String() + "/x/..", false},
		{"dot-dot as substring of a file name still passes", "backups/" + me.String() + "/x/report..final.pdf", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := storagePathAllowed(tt.path, me); got != tt.want {
				t.Errorf("storagePathAllowed(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// TestDocumentIDGuards: every document route taking an :id must 400 a
// non-UUID before touching authorization or the database — the parse is the
// first line of each handler, so a regression here would surface as a nil-DB
// panic rather than a wrong status.
func TestDocumentIDGuards(t *testing.T) {
	noUserService(t)
	uid := uuid.Must(uuid.NewV7())
	for _, tt := range []struct {
		name, method, pattern, target string
		h                             gin.HandlerFunc
	}{
		{"get", http.MethodGet, "/api/documents/:id", "/api/documents/not-a-uuid", GetDocument},
		{"download", http.MethodGet, "/api/documents/:id/download", "/api/documents/not-a-uuid/download", DownloadDocument},
		{"patch", http.MethodPatch, "/api/documents/:id", "/api/documents/not-a-uuid", UpdateDocument},
		{"delete", http.MethodDelete, "/api/documents/:id", "/api/documents/not-a-uuid", DeleteDocument},
		{"restore", http.MethodPost, "/api/documents/:id/restore", "/api/documents/not-a-uuid/restore", RestoreDocument},
		{"purge", http.MethodDelete, "/api/documents/:id/permanent", "/api/documents/not-a-uuid/permanent", PurgeDocument},
		{"attach tag", http.MethodPost, "/api/documents/:id/tags", "/api/documents/not-a-uuid/tags", AttachTag},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := doAuthed(t, uid, tt.method, tt.pattern, tt.h, tt.target, "")
			wantErrEnvelope(t, w, http.StatusBadRequest, response.CodeInvalidID)
		})
	}

	// DetachTag has a second identifier with its own code: a valid document id
	// but a malformed tag id must fail on the tag, not fall through.
	t.Run("detach tag id", func(t *testing.T) {
		target := "/api/documents/" + uuid.Must(uuid.NewV7()).String() + "/tags/not-a-uuid"
		w := doAuthed(t, uid, http.MethodDelete, "/api/documents/:id/tags/:tagId", DetachTag, target, "")
		wantErrEnvelope(t, w, http.StatusBadRequest, "INVALID_TAG")
	})
}

// TestCreateDocumentBodyGuards covers CreateDocument's pre-DB rejections:
// binding validation, the name-required rule, and the storagePath ownership
// contract (the HANDLER-level half of what TestStoragePathAllowed pins on the
// pure function). All rows are personal-scope, so no membership lookup runs.
func TestCreateDocumentBodyGuards(t *testing.T) {
	noUserService(t)
	uid := uuid.Must(uuid.NewV7())
	other := uuid.Must(uuid.NewV7())
	post := func(body string) *httptest.ResponseRecorder {
		return doAuthed(t, uid, http.MethodPost, "/api/documents", CreateDocument, "/api/documents", body)
	}

	for _, tt := range []struct {
		name, body, code string
	}{
		{"no body at all", "", response.CodeInvalidInput},
		{"malformed JSON", "{not json", response.CodeInvalidInput},
		{"missing name", `{}`, response.CodeInvalidInput},
		{"whitespace-only name", `{"name":"   "}`, "INVALID_NAME"},
		{"non-uuid folderId", `{"name":"a","folderId":"nope"}`, response.CodeInvalidInput},
		{"status outside the enum", `{"name":"a","status":"archived"}`, response.CodeInvalidInput},
		// Ownership contract: registering a key under another user's backups/
		// prefix is 400 INVALID_INPUT, decided before the insert would run.
		{"foreign backups path", fmt.Sprintf(`{"name":"a","storagePath":"backups/%s/x/f.pdf"}`, other), response.CodeInvalidInput},
		{"dot-dot backups path", fmt.Sprintf(`{"name":"a","storagePath":"backups/%s/../%s/f.pdf"}`, uid, other), response.CodeInvalidInput},
	} {
		t.Run(tt.name, func(t *testing.T) {
			wantErrEnvelope(t, post(tt.body), http.StatusBadRequest, tt.code)
		})
	}
}

// TestUpdateDocumentBodyGuards covers UpdateDocument's pre-DB rejections.
func TestUpdateDocumentBodyGuards(t *testing.T) {
	noUserService(t)
	uid := uuid.Must(uuid.NewV7())
	target := "/api/documents/" + uuid.Must(uuid.NewV7()).String()
	patch := func(body string) *httptest.ResponseRecorder {
		return doAuthed(t, uid, http.MethodPatch, "/api/documents/:id", UpdateDocument, target, body)
	}

	for _, tt := range []struct {
		name, body, code string
	}{
		{"no body at all", "", response.CodeInvalidInput},
		{"malformed JSON", "{", response.CodeInvalidInput},
		// A PATCH that names no field is a client bug, not a no-op success.
		{"empty patch", `{}`, "NO_CHANGES"},
		{"name cleared to whitespace", `{"name":"  "}`, "INVALID_NAME"},
		{"non-uuid folderId", `{"folderId":"nope"}`, response.CodeInvalidInput},
		{"status outside the enum", `{"status":"archived"}`, response.CodeInvalidInput},
	} {
		t.Run(tt.name, func(t *testing.T) {
			wantErrEnvelope(t, patch(tt.body), http.StatusBadRequest, tt.code)
		})
	}
}

// TestListDocumentsGuards: the two list-level rejections that run before any
// query is built — a malformed orgId and a malformed updatedSince. The
// updatedSince rejection is deliberate API shape (Phase 9): silently ignoring
// it would make a sync client full-list and re-download everything.
func TestListDocumentsGuards(t *testing.T) {
	noUserService(t)
	uid := uuid.Must(uuid.NewV7())
	t.Run("invalid orgId", func(t *testing.T) {
		w := doAuthed(t, uid, http.MethodGet, "/api/documents", ListDocuments, "/api/documents?orgId=not-a-uuid", "")
		wantErrEnvelope(t, w, http.StatusBadRequest, "INVALID_ORG")
	})
	t.Run("invalid updatedSince", func(t *testing.T) {
		w := doAuthed(t, uid, http.MethodGet, "/api/documents", ListDocuments, "/api/documents?updatedSince=yesterday", "")
		wantErrEnvelope(t, w, http.StatusBadRequest, response.CodeInvalidInput)
	})
}

// TestAttachTagBodyGuards: AttachTag binds the body before resolving scope.
func TestAttachTagBodyGuards(t *testing.T) {
	noUserService(t)
	uid := uuid.Must(uuid.NewV7())
	target := "/api/documents/" + uuid.Must(uuid.NewV7()).String() + "/tags"
	for _, tt := range []struct {
		name, body string
	}{
		{"no body", ""},
		{"missing tagId", `{}`},
		{"non-uuid tagId", `{"tagId":"nope"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := doAuthed(t, uid, http.MethodPost, "/api/documents/:id/tags", AttachTag, target, tt.body)
			wantErrEnvelope(t, w, http.StatusBadRequest, response.CodeInvalidInput)
		})
	}
}

// orgContext builds a request-bearing, authenticated gin context for direct
// resolveOrg calls (the pattern job-service uses for authorizeJobAccess).
func orgContext(t *testing.T, uid uuid.UUID) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/documents", nil)
	authverify.SetGinAuth(c, authverify.AuthContext{UserID: uid.String()})
	return c, w
}

// TestResolveOrg is the org-scoping authorization matrix, run against a stub
// user-service (USER_SERVICE_URL seam). Subtest order is load-bearing: the
// upstream-failure rows come last and there are only two of them, so
// orgclient's package-level circuit breaker (trips at 5 consecutive failures)
// can never open mid-suite regardless of -shuffle, which only reorders
// top-level tests.
func TestResolveOrg(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	orgID := uuid.Must(uuid.NewV7())

	t.Run("no orgId means personal scope, no lookup", func(t *testing.T) {
		noUserService(t)
		c, w := orgContext(t, uid)
		got, ok := resolveOrg(c, "  ", "viewer")
		if !ok || got != nil {
			t.Errorf("resolveOrg(\"\") = (%v, %v), want (nil, true)", got, ok)
		}
		if w.Body.Len() != 0 {
			t.Errorf("personal scope wrote a response: %q", w.Body.String())
		}
	})

	t.Run("malformed orgId rejected before any lookup", func(t *testing.T) {
		noUserService(t)
		c, w := orgContext(t, uid)
		if _, ok := resolveOrg(c, "not-a-uuid", "viewer"); ok {
			t.Fatal("malformed orgId resolved")
		}
		wantErrEnvelope(t, w, http.StatusBadRequest, "INVALID_ORG")
	})

	// Role rank enforcement: reads need viewer+, writes editor+.
	for _, tt := range []struct {
		role, min string
		allowed   bool
	}{
		{"viewer", "viewer", true},
		{"viewer", "editor", false},
		{"editor", "editor", true},
		{"admin", "editor", true},
		{"owner", "editor", true},
		// A role this service does not know ranks 0 and must never satisfy
		// any minimum — unknown must fail closed, not open.
		{"superadmin", "viewer", false},
	} {
		t.Run(fmt.Sprintf("role %s vs min %s", tt.role, tt.min), func(t *testing.T) {
			withUserService(t, membershipRole(tt.role))
			c, w := orgContext(t, uid)
			got, ok := resolveOrg(c, orgID.String(), tt.min)
			if tt.allowed {
				if !ok || got == nil || *got != orgID {
					t.Fatalf("resolveOrg = (%v, %v), want (&%s, true)", got, ok, orgID)
				}
				return
			}
			if ok {
				t.Fatal("insufficient role resolved the org")
			}
			wantErrEnvelope(t, w, http.StatusForbidden, response.CodeForbidden)
		})
	}

	// NOTE: a non-member gets 404 "Organization not found.", NOT 403. This is
	// deliberate: answering 403 would confirm to an outsider that the org id
	// exists. user-service's own 403 is folded into the same 404 for the same
	// reason (orgclient treats both as "not a member").
	for _, upstream := range []int{http.StatusNotFound, http.StatusForbidden} {
		t.Run(fmt.Sprintf("non-member (upstream %d) masked as 404", upstream), func(t *testing.T) {
			withUserService(t, membershipStatus(upstream))
			c, w := orgContext(t, uid)
			if _, ok := resolveOrg(c, orgID.String(), "viewer"); ok {
				t.Fatal("non-member resolved the org")
			}
			wantErrEnvelope(t, w, http.StatusNotFound, response.CodeNotFound)
		})
	}

	// Dependency-failure rows (the only breaker-counted failures — keep last,
	// keep at two; see the function comment). Authorization fails SAFE: an
	// unreachable user-service is a 502, never silent personal-scope access.
	t.Run("user-service 500 fails safe with 502", func(t *testing.T) {
		withUserService(t, membershipStatus(http.StatusInternalServerError))
		c, w := orgContext(t, uid)
		if _, ok := resolveOrg(c, orgID.String(), "viewer"); ok {
			t.Fatal("org resolved while user-service was failing")
		}
		wantErrEnvelope(t, w, http.StatusBadGateway, "ORG_CHECK_FAILED")
	})

	t.Run("user-service unreachable fails safe with 502", func(t *testing.T) {
		srv := httptest.NewServer(membershipStatus(http.StatusOK))
		srv.Close() // connection refused from here on
		t.Setenv("USER_SERVICE_URL", srv.URL)
		c, w := orgContext(t, uid)
		if _, ok := resolveOrg(c, orgID.String(), "viewer"); ok {
			t.Fatal("org resolved while user-service was unreachable")
		}
		wantErrEnvelope(t, w, http.StatusBadGateway, "ORG_CHECK_FAILED")
	})
}

// TestOrgScopedHandlersStopAtResolveOrg proves resolveOrg is wired in front of
// the database in real handlers: an org-scoped request that fails membership
// or role must produce the resolveOrg response, reaching no query (nil DB).
func TestOrgScopedHandlersStopAtResolveOrg(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	orgID := uuid.Must(uuid.NewV7()).String()

	t.Run("read route, non-member, 404", func(t *testing.T) {
		withUserService(t, membershipStatus(http.StatusNotFound))
		w := doAuthed(t, uid, http.MethodGet, "/api/tags", ListTags, "/api/tags?orgId="+orgID, "")
		wantErrEnvelope(t, w, http.StatusNotFound, response.CodeNotFound)
	})

	t.Run("write route, viewer role, 403", func(t *testing.T) {
		withUserService(t, membershipRole("viewer"))
		target := "/api/tags/" + uuid.Must(uuid.NewV7()).String() + "?orgId=" + orgID
		w := doAuthed(t, uid, http.MethodDelete, "/api/tags/:id", DeleteTag, target, "")
		wantErrEnvelope(t, w, http.StatusForbidden, response.CodeForbidden)
	})
}

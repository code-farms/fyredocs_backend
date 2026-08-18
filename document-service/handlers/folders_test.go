// Tests for folder handlers' pre-DB guard paths (offline, nil DB — see
// harness_test.go).
package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"fyredocs/shared/response"
)

func TestCreateFolderGuards(t *testing.T) {
	noUserService(t)
	uid := uuid.Must(uuid.NewV7())
	for _, tt := range []struct {
		name, body, code string
	}{
		{"no body", "", response.CodeInvalidInput},
		{"malformed JSON", "{", response.CodeInvalidInput},
		{"missing name", `{}`, "INVALID_NAME"},
		{"whitespace-only name", `{"name":"  "}`, "INVALID_NAME"},
		{"non-uuid parentId", `{"name":"docs","parentId":"nope"}`, response.CodeInvalidInput},
		{"non-uuid organizationId", `{"name":"docs","organizationId":"nope"}`, response.CodeInvalidInput},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := doAuthed(t, uid, http.MethodPost, "/api/folders", CreateFolder, "/api/folders", tt.body)
			wantErrEnvelope(t, w, http.StatusBadRequest, tt.code)
		})
	}
}

func TestUpdateFolderGuards(t *testing.T) {
	noUserService(t)
	uid := uuid.Must(uuid.NewV7())
	folderID := uuid.Must(uuid.NewV7())
	patch := func(target, body string) *httptest.ResponseRecorder {
		return doAuthed(t, uid, http.MethodPatch, "/api/folders/:id", UpdateFolder, target, body)
	}

	t.Run("invalid id", func(t *testing.T) {
		wantErrEnvelope(t, patch("/api/folders/not-a-uuid", `{"name":"x"}`), http.StatusBadRequest, response.CodeInvalidID)
	})
	t.Run("invalid orgId", func(t *testing.T) {
		w := patch("/api/folders/"+folderID.String()+"?orgId=nope", `{"name":"x"}`)
		wantErrEnvelope(t, w, http.StatusBadRequest, "INVALID_ORG")
	})
	t.Run("no body", func(t *testing.T) {
		wantErrEnvelope(t, patch("/api/folders/"+folderID.String(), ""), http.StatusBadRequest, response.CodeInvalidInput)
	})
	t.Run("empty patch", func(t *testing.T) {
		wantErrEnvelope(t, patch("/api/folders/"+folderID.String(), `{}`), http.StatusBadRequest, "NO_CHANGES")
	})
	t.Run("non-uuid parentId", func(t *testing.T) {
		w := patch("/api/folders/"+folderID.String(), `{"parentId":"nope"}`)
		wantErrEnvelope(t, w, http.StatusBadRequest, response.CodeInvalidInput)
	})
	// A folder that is its own parent would orphan an entire subtree into a
	// cycle; the direct self-reference is the only case checkable without a
	// tree walk, and it is rejected before the update runs.
	t.Run("folder as its own parent", func(t *testing.T) {
		w := patch("/api/folders/"+folderID.String(), `{"parentId":"`+folderID.String()+`"}`)
		wantErrEnvelope(t, w, http.StatusBadRequest, "INVALID_PARENT")
	})
}

func TestDeleteFolderGuards(t *testing.T) {
	noUserService(t)
	uid := uuid.Must(uuid.NewV7())
	t.Run("invalid id", func(t *testing.T) {
		w := doAuthed(t, uid, http.MethodDelete, "/api/folders/:id", DeleteFolder, "/api/folders/not-a-uuid", "")
		wantErrEnvelope(t, w, http.StatusBadRequest, response.CodeInvalidID)
	})
	t.Run("invalid orgId", func(t *testing.T) {
		target := "/api/folders/" + uuid.Must(uuid.NewV7()).String() + "?orgId=nope"
		w := doAuthed(t, uid, http.MethodDelete, "/api/folders/:id", DeleteFolder, target, "")
		wantErrEnvelope(t, w, http.StatusBadRequest, "INVALID_ORG")
	})
}

// Tests for tag handlers' pre-DB guard paths (offline, nil DB — see
// harness_test.go). Org-role enforcement on the tag write path is covered by
// TestOrgScopedHandlersStopAtResolveOrg in documents_test.go.
package handlers

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"fyredocs/shared/response"
)

func TestCreateTagGuards(t *testing.T) {
	noUserService(t)
	uid := uuid.Must(uuid.NewV7())
	for _, tt := range []struct {
		name, body, code string
	}{
		{"no body", "", response.CodeInvalidInput},
		{"malformed JSON", "{", response.CodeInvalidInput},
		{"missing name", `{}`, "INVALID_NAME"},
		{"whitespace-only name", `{"name":" "}`, "INVALID_NAME"},
		{"name over 100 chars", `{"name":"` + strings.Repeat("a", 101) + `"}`, response.CodeInvalidInput},
		{"color over 32 chars", `{"name":"x","color":"` + strings.Repeat("f", 33) + `"}`, response.CodeInvalidInput},
		{"non-uuid organizationId", `{"name":"x","organizationId":"nope"}`, response.CodeInvalidInput},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := doAuthed(t, uid, http.MethodPost, "/api/tags", CreateTag, "/api/tags", tt.body)
			wantErrEnvelope(t, w, http.StatusBadRequest, tt.code)
		})
	}
}

func TestDeleteTagGuards(t *testing.T) {
	noUserService(t)
	uid := uuid.Must(uuid.NewV7())
	t.Run("invalid id", func(t *testing.T) {
		w := doAuthed(t, uid, http.MethodDelete, "/api/tags/:id", DeleteTag, "/api/tags/not-a-uuid", "")
		wantErrEnvelope(t, w, http.StatusBadRequest, response.CodeInvalidID)
	})
	t.Run("invalid orgId", func(t *testing.T) {
		target := "/api/tags/" + uuid.Must(uuid.NewV7()).String() + "?orgId=nope"
		w := doAuthed(t, uid, http.MethodDelete, "/api/tags/:id", DeleteTag, target, "")
		wantErrEnvelope(t, w, http.StatusBadRequest, "INVALID_ORG")
	})
}

func TestListTagsInvalidOrg(t *testing.T) {
	noUserService(t)
	uid := uuid.Must(uuid.NewV7())
	w := doAuthed(t, uid, http.MethodGet, "/api/tags", ListTags, "/api/tags?orgId=nope", "")
	wantErrEnvelope(t, w, http.StatusBadRequest, "INVALID_ORG")
}

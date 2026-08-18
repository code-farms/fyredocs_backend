// Tests for the workspace-hint handler's pre-DB guard paths (offline, nil DB —
// see harness_test.go).
//
// Deliberately NOT covered here: a valid jobId with an empty organizationId
// (the clear-hint path) and a member with editor+ (the upsert path) both reach
// the database immediately and belong to the integration tier.
package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"fyredocs/shared/response"
)

func TestSetJobWorkspaceHintGuards(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	post := func(body string) *httptest.ResponseRecorder {
		return doAuthed(t, uid, http.MethodPost, "/api/documents/workspace-hint", SetJobWorkspaceHint,
			"/api/documents/workspace-hint", body)
	}

	t.Run("body validation", func(t *testing.T) {
		noUserService(t)
		for _, tt := range []struct {
			name, body string
		}{
			{"no body", ""},
			{"malformed JSON", "{"},
			{"missing jobId", `{}`},
			{"non-uuid jobId", `{"jobId":"nope"}`},
			{"non-uuid organizationId", fmt.Sprintf(`{"jobId":"%s","organizationId":"nope"}`, uuid.Must(uuid.NewV7()))},
		} {
			t.Run(tt.name, func(t *testing.T) {
				wantErrEnvelope(t, post(tt.body), http.StatusBadRequest, response.CodeInvalidInput)
			})
		}
	})

	// Filing a job into an org is a WRITE into that org's workspace, so the
	// hint requires editor+ — a viewer must not be able to route documents
	// into an org they can only read.
	t.Run("viewer role cannot set an org hint", func(t *testing.T) {
		withUserService(t, membershipRole("viewer"))
		body := fmt.Sprintf(`{"jobId":"%s","organizationId":"%s"}`,
			uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()))
		wantErrEnvelope(t, post(body), http.StatusForbidden, response.CodeForbidden)
	})

	t.Run("non-member masked as 404", func(t *testing.T) {
		withUserService(t, membershipStatus(http.StatusNotFound))
		body := fmt.Sprintf(`{"jobId":"%s","organizationId":"%s"}`,
			uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()))
		wantErrEnvelope(t, post(body), http.StatusNotFound, response.CodeNotFound)
	})
}

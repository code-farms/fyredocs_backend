package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"fyredocs/shared/authverify"
)

// errorCode extracts error.code from the standard response envelope (CLAUDE.md §7)
// so tests can distinguish which guard fired when two guards share a status.
func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response envelope: %v (body %q)", err, w.Body.String())
	}
	return body.Error.Code
}

// orgsRouter mounts the org handlers behind RequireUser exactly as
// routes.SetupRouter composes them, optionally seeding a verified identity the
// way the auth middleware would. models.DB is nil in unit tests, so every
// request sent through this router must be stopped by a pre-DB guard —
// reaching a query panics and fails the test loudly rather than passing vacuously.
func orgsRouter(authCtx *authverify.AuthContext) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if authCtx != nil {
		r.Use(func(c *gin.Context) {
			authverify.SetGinAuth(c, *authCtx)
			c.Next()
		})
	}
	g := r.Group("/api/orgs")
	g.Use(RequireUser())
	{
		g.POST("", CreateOrganization)
		g.GET("/:id", GetOrganization)
		g.GET("/:id/members", ListMembers)
		g.POST("/:id/members", AddMember)
		g.PATCH("/:id/members/:userId", UpdateMemberRole)
		g.DELETE("/:id/members/:userId", RemoveMember)
	}
	return r
}

// verifiedUser fabricates the AuthContext a verified JWT would produce.
func verifiedUser() *authverify.AuthContext {
	return &authverify.AuthContext{UserID: uuid.Must(uuid.NewV7()).String()}
}

// doJSON serves one request with an optional JSON body and returns the recorder.
func doJSON(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestCreateOrganizationRejectsInvalidNames covers the pre-DB validation of the
// create body: binding (required, max=120) plus the post-trim emptiness check.
// A name that survives these guards goes straight to the DB transaction, so the
// happy path is integration-tier by construction.
func TestCreateOrganizationRejectsInvalidNames(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantCode string
	}{
		{"missing name field", `{}`, ""},
		{"empty name", `{"name":""}`, ""},
		{"whitespace-only name survives binding, dies on trim", `{"name":"   "}`, "INVALID_NAME"},
		{"name over 120 chars", `{"name":"` + strings.Repeat("a", 121) + `"}`, ""},
		{"malformed json", `{"name":`, ""},
		{"no body at all", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := orgsRouter(verifiedUser())
			w := doJSON(r, http.MethodPost, "/api/orgs", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d (body %q)", w.Code, w.Body.String())
			}
			if tc.wantCode != "" {
				if got := errorCode(t, w); got != tc.wantCode {
					t.Errorf("error code = %q, want %q", got, tc.wantCode)
				}
			}
		})
	}
}

// TestCreateOrganizationRequiresAuth pins that org creation is unreachable
// without a verified identity — RequireUser aborts before the handler runs.
func TestCreateOrganizationRequiresAuth(t *testing.T) {
	r := orgsRouter(nil)
	w := doJSON(r, http.MethodPost, "/api/orgs", `{"name":"Acme"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// TestOrgScopedRoutesRejectInvalidOrgID pins that every :id route parses the
// organization id BEFORE anything else — including before the request body is
// read. The AddMember/UpdateMemberRole rows deliberately carry a role of
// "owner": getting INVALID_ID back (not INVALID_ROLE) proves the ordering, and
// proves a bogus org id can never reach the membership lookup.
func TestOrgScopedRoutesRejectInvalidOrgID(t *testing.T) {
	member := uuid.Must(uuid.NewV7()).String()
	cases := []struct {
		name, method, path, body string
	}{
		{"get org", http.MethodGet, "/api/orgs/not-a-uuid", ""},
		{"list members", http.MethodGet, "/api/orgs/not-a-uuid/members", ""},
		{"add member", http.MethodPost, "/api/orgs/not-a-uuid/members", `{"userId":"` + member + `","role":"owner"}`},
		{"update member role", http.MethodPatch, "/api/orgs/not-a-uuid/members/" + member, `{"role":"owner"}`},
		{"remove member", http.MethodDelete, "/api/orgs/not-a-uuid/members/" + member, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := orgsRouter(verifiedUser())
			w := doJSON(r, tc.method, tc.path, tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d (body %q)", w.Code, w.Body.String())
			}
			if got := errorCode(t, w); got != "INVALID_ID" {
				t.Errorf("error code = %q, want INVALID_ID", got)
			}
		})
	}
}

// TestOrgScopedRoutesRequireAuth pins the 401 for member-mutation routes with a
// syntactically valid org id: RequireUser must abort before orgParam ever runs,
// because orgParam's membership lookup needs the database.
func TestOrgScopedRoutesRequireAuth(t *testing.T) {
	org := uuid.Must(uuid.NewV7()).String()
	member := uuid.Must(uuid.NewV7()).String()
	cases := []struct {
		name, method, path string
	}{
		{"get org", http.MethodGet, "/api/orgs/" + org},
		{"list members", http.MethodGet, "/api/orgs/" + org + "/members"},
		{"add member", http.MethodPost, "/api/orgs/" + org + "/members"},
		{"update member role", http.MethodPatch, "/api/orgs/" + org + "/members/" + member},
		{"remove member", http.MethodDelete, "/api/orgs/" + org + "/members/" + member},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := orgsRouter(nil)
			w := doJSON(r, tc.method, tc.path, "")
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", w.Code)
			}
		})
	}
}

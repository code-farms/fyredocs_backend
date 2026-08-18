//go:build integration

// Integration tests for the organization membership privilege-escalation
// matrix over real rows: who may add, re-role, and remove members; the
// owner-protection rules; the member-vs-404 disclosure contract; and the
// mesh-only /internal/v1 lookup. Every handler here commits through
// models.DB (CreateOrganization even opens its own transaction), so these
// tests use Truncate isolation, never dbtest.Tx (see testing-strategy.md).
package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"fyredocs/shared/authverify"
	"fyredocs/shared/config"
	"fyredocs/shared/database/dbtest"

	"user-service/internal/migrations"
	"user-service/internal/models"
)

// withDB applies this service's migrations to the shared test database and
// swaps the package-global handle for the test's duration. The service name
// must stay "user-service" — it selects the goose history table production
// writes (models.Migrate). The truncated set is exactly this service's own
// tables from the migrations baseline (00001_baseline.sql).
func withDB(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := dbtest.Open(t, "user-service", migrations.FS)
	prev := models.DB
	models.DB = db
	t.Cleanup(func() { models.DB = prev })
	dbtest.Truncate(t, db, "organizations", "memberships")
}

// asUser returns the org router with a verified identity for uid, reusing the
// unit suite's orgsRouter so the middleware composition under test is the
// same one routes.SetupRouter builds.
func asUser(uid uuid.UUID) *gin.Engine {
	return orgsRouter(&authverify.AuthContext{UserID: uid.String()})
}

// seedOrg creates an organization and its owner membership directly in the
// database — the same two rows CreateOrganization commits — so tests can
// exercise one handler without depending on another's happy path.
func seedOrg(t *testing.T, owner uuid.UUID) models.Organization {
	t.Helper()
	org := models.Organization{
		Name: "Seeded Org",
		// Random suffix keeps the unique slug index happy across seeds.
		Slug:        "seeded-org-" + uuid.Must(uuid.NewV7()).String(),
		OwnerUserID: owner,
		PlanName:    "free",
	}
	if err := models.DB.Create(&org).Error; err != nil {
		t.Fatalf("seed org: %v", err)
	}
	seedMember(t, org.ID, owner, models.RoleOwner)
	return org
}

// seedMember inserts a membership row with the given role.
func seedMember(t *testing.T, orgID, uid uuid.UUID, role string) {
	t.Helper()
	m := models.Membership{OrganizationID: orgID, UserID: uid, Role: role}
	if err := models.DB.Create(&m).Error; err != nil {
		t.Fatalf("seed member %s as %s: %v", uid, role, err)
	}
}

// roleOf reads a membership role straight from the table; ok=false means no
// row exists. Asserting on rows rather than responses is the point of this
// suite — a 403 only counts if the database really did not change.
func roleOf(t *testing.T, orgID, uid uuid.UUID) (string, bool) {
	t.Helper()
	var m models.Membership
	err := models.DB.Where("organization_id = ? AND user_id = ?", orgID, uid).First(&m).Error
	if err != nil {
		return "", false
	}
	return m.Role, true
}

func TestCreateOrganization_CreatorBecomesOwnerMember(t *testing.T) {
	withDB(t)
	uid := uuid.Must(uuid.NewV7())

	w := doJSON(asUser(uid), http.MethodPost, "/api/orgs", `{"name":"Acme Legal"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create org: code = %d, body = %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			ID   uuid.UUID `json:"id"`
			Slug string    `json:"slug"`
			Role string    `json:"role"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode create response: %v\n%s", err, w.Body.String())
	}
	if resp.Data.Role != models.RoleOwner {
		t.Errorf("response role = %q, want owner", resp.Data.Role)
	}

	var org models.Organization
	if err := models.DB.First(&org, "id = ?", resp.Data.ID).Error; err != nil {
		t.Fatalf("org row missing: %v", err)
	}
	if org.OwnerUserID != uid {
		t.Errorf("org.OwnerUserID = %s, want caller %s (role must come from identity, never the body)", org.OwnerUserID, uid)
	}
	if org.PlanName != "free" {
		t.Errorf("org.PlanName = %q, want free", org.PlanName)
	}
	if got, ok := roleOf(t, org.ID, uid); !ok || got != models.RoleOwner {
		t.Errorf("creator membership = (%q, %v), want (owner, true)", got, ok)
	}
}

// TestAddMember_CallerRoleMatrix pins who may grant membership: RoleAtLeast
// admin (owner, admin) succeeds; editor and viewer are stopped at 403 with no
// row written. This is the privilege-escalation gate — an editor who could
// add members could add themself a second admin identity.
func TestAddMember_CallerRoleMatrix(t *testing.T) {
	withDB(t)
	owner := uuid.Must(uuid.NewV7())
	org := seedOrg(t, owner)

	admin := uuid.Must(uuid.NewV7())
	editor := uuid.Must(uuid.NewV7())
	viewer := uuid.Must(uuid.NewV7())
	seedMember(t, org.ID, admin, models.RoleAdmin)
	seedMember(t, org.ID, editor, models.RoleEditor)
	seedMember(t, org.ID, viewer, models.RoleViewer)

	cases := []struct {
		name     string
		caller   uuid.UUID
		wantCode int
	}{
		{"owner may add", owner, http.StatusCreated},
		{"admin may add", admin, http.StatusCreated},
		{"editor denied", editor, http.StatusForbidden},
		{"viewer denied", viewer, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := uuid.Must(uuid.NewV7())
			w := doJSON(asUser(tc.caller), http.MethodPost, "/api/orgs/"+org.ID.String()+"/members",
				`{"userId":"`+target.String()+`","role":"editor"}`)
			if w.Code != tc.wantCode {
				t.Fatalf("code = %d, want %d (body %s)", w.Code, tc.wantCode, w.Body.String())
			}
			role, exists := roleOf(t, org.ID, target)
			if tc.wantCode == http.StatusCreated {
				if !exists || role != models.RoleEditor {
					t.Errorf("membership after add = (%q, %v), want (editor, true)", role, exists)
				}
			} else {
				if got := errorCode(t, w); got != "AUTH_FORBIDDEN" {
					t.Errorf("error code = %q, want AUTH_FORBIDDEN", got)
				}
				if exists {
					t.Errorf("denied add still wrote a membership row (role %q)", role)
				}
			}
		})
	}
}

// TestAddMember_OwnerRoleGrantRejectedAtBinding pins WHERE the "no granting
// owner" rule fires over HTTP: the binding tag (oneof=admin editor viewer)
// rejects the body with 400 INVALID_INPUT before the handler's own
// newRole == RoleOwner check (400 INVALID_ROLE) can run — that handler check
// is the defense-in-depth backstop, unreachable through JSON binding.
func TestAddMember_OwnerRoleGrantRejectedAtBinding(t *testing.T) {
	withDB(t)
	owner := uuid.Must(uuid.NewV7())
	org := seedOrg(t, owner)
	target := uuid.Must(uuid.NewV7())

	w := doJSON(asUser(owner), http.MethodPost, "/api/orgs/"+org.ID.String()+"/members",
		`{"userId":"`+target.String()+`","role":"owner"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 (body %s)", w.Code, w.Body.String())
	}
	if got := errorCode(t, w); got != "INVALID_INPUT" {
		t.Errorf("error code = %q, want INVALID_INPUT (binding oneof is the live gate)", got)
	}
	if _, exists := roleOf(t, org.ID, target); exists {
		t.Error("rejected owner-grant still wrote a membership row")
	}
}

// TestAddMember_CannotRetargetOwnersMembership pins the owner-protection rule
// on the AddMember upsert path: re-adding the owner with a lower role must
// not demote them (which would orphan the org's top privilege).
func TestAddMember_CannotRetargetOwnersMembership(t *testing.T) {
	withDB(t)
	owner := uuid.Must(uuid.NewV7())
	org := seedOrg(t, owner)
	admin := uuid.Must(uuid.NewV7())
	seedMember(t, org.ID, admin, models.RoleAdmin)

	w := doJSON(asUser(admin), http.MethodPost, "/api/orgs/"+org.ID.String()+"/members",
		`{"userId":"`+owner.String()+`","role":"viewer"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403 (body %s)", w.Code, w.Body.String())
	}
	if role, _ := roleOf(t, org.ID, owner); role != models.RoleOwner {
		t.Errorf("owner role after denied demotion = %q, want owner", role)
	}
}

// TestAddMember_ReAddUpdatesRoleWithoutDuplicateRow pins the idempotent-add
// contract: the second add of the same user updates the role in place (200,
// not 201) and the (org, user) unique index keeps a single row.
func TestAddMember_ReAddUpdatesRoleWithoutDuplicateRow(t *testing.T) {
	withDB(t)
	owner := uuid.Must(uuid.NewV7())
	org := seedOrg(t, owner)
	target := uuid.Must(uuid.NewV7())

	first := doJSON(asUser(owner), http.MethodPost, "/api/orgs/"+org.ID.String()+"/members",
		`{"userId":"`+target.String()+`","role":"viewer"}`)
	if first.Code != http.StatusCreated {
		t.Fatalf("first add: code = %d (body %s)", first.Code, first.Body.String())
	}
	second := doJSON(asUser(owner), http.MethodPost, "/api/orgs/"+org.ID.String()+"/members",
		`{"userId":"`+target.String()+`","role":"editor"}`)
	if second.Code != http.StatusOK {
		t.Fatalf("re-add: code = %d, want 200 update (body %s)", second.Code, second.Body.String())
	}

	var count int64
	if err := models.DB.Model(&models.Membership{}).
		Where("organization_id = ? AND user_id = ?", org.ID, target).Count(&count).Error; err != nil {
		t.Fatalf("count memberships: %v", err)
	}
	if count != 1 {
		t.Errorf("membership rows = %d, want 1", count)
	}
	if role, _ := roleOf(t, org.ID, target); role != models.RoleEditor {
		t.Errorf("role after re-add = %q, want editor", role)
	}
}

// TestAddMember_DefaultRoleIsViewer pins the least-privilege default when the
// body names no role.
func TestAddMember_DefaultRoleIsViewer(t *testing.T) {
	withDB(t)
	owner := uuid.Must(uuid.NewV7())
	org := seedOrg(t, owner)
	target := uuid.Must(uuid.NewV7())

	w := doJSON(asUser(owner), http.MethodPost, "/api/orgs/"+org.ID.String()+"/members",
		`{"userId":"`+target.String()+`"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("code = %d (body %s)", w.Code, w.Body.String())
	}
	if role, _ := roleOf(t, org.ID, target); role != models.RoleViewer {
		t.Errorf("default role = %q, want viewer", role)
	}
}

// TestUpdateMemberRole_EditorCannotSelfPromote is the core escalation row:
// an editor PATCHing their own membership to admin must be stopped by the
// caller-role gate (403) with the row untouched.
func TestUpdateMemberRole_EditorCannotSelfPromote(t *testing.T) {
	withDB(t)
	owner := uuid.Must(uuid.NewV7())
	org := seedOrg(t, owner)
	editor := uuid.Must(uuid.NewV7())
	seedMember(t, org.ID, editor, models.RoleEditor)

	w := doJSON(asUser(editor), http.MethodPatch,
		"/api/orgs/"+org.ID.String()+"/members/"+editor.String(), `{"role":"admin"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403 (body %s)", w.Code, w.Body.String())
	}
	if got := errorCode(t, w); got != "AUTH_FORBIDDEN" {
		t.Errorf("error code = %q, want AUTH_FORBIDDEN", got)
	}
	if role, _ := roleOf(t, org.ID, editor); role != models.RoleEditor {
		t.Errorf("editor role after denied self-promotion = %q, want editor", role)
	}
}

// TestUpdateMemberRole_AdminPromotesViewerToEditor pins the allowed half of
// the matrix: RoleAtLeast(admin) callers may move a non-owner member between
// admin/editor/viewer.
func TestUpdateMemberRole_AdminPromotesViewerToEditor(t *testing.T) {
	withDB(t)
	owner := uuid.Must(uuid.NewV7())
	org := seedOrg(t, owner)
	admin := uuid.Must(uuid.NewV7())
	viewer := uuid.Must(uuid.NewV7())
	seedMember(t, org.ID, admin, models.RoleAdmin)
	seedMember(t, org.ID, viewer, models.RoleViewer)

	w := doJSON(asUser(admin), http.MethodPatch,
		"/api/orgs/"+org.ID.String()+"/members/"+viewer.String(), `{"role":"editor"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	if role, _ := roleOf(t, org.ID, viewer); role != models.RoleEditor {
		t.Errorf("role after promotion = %q, want editor", role)
	}
}

// TestUpdateMemberRole_OwnerRoleGrantRejectedAtBinding: as with AddMember,
// PATCHing anyone TO owner dies at the binding oneof (400 INVALID_INPUT); the
// handler's INVALID_ROLE branch is an unreachable-over-JSON backstop.
func TestUpdateMemberRole_OwnerRoleGrantRejectedAtBinding(t *testing.T) {
	withDB(t)
	owner := uuid.Must(uuid.NewV7())
	org := seedOrg(t, owner)
	admin := uuid.Must(uuid.NewV7())
	viewer := uuid.Must(uuid.NewV7())
	seedMember(t, org.ID, admin, models.RoleAdmin)
	seedMember(t, org.ID, viewer, models.RoleViewer)

	w := doJSON(asUser(admin), http.MethodPatch,
		"/api/orgs/"+org.ID.String()+"/members/"+viewer.String(), `{"role":"owner"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 (body %s)", w.Code, w.Body.String())
	}
	if got := errorCode(t, w); got != "INVALID_INPUT" {
		t.Errorf("error code = %q, want INVALID_INPUT (binding oneof is the live gate)", got)
	}
	if role, _ := roleOf(t, org.ID, viewer); role != models.RoleViewer {
		t.Errorf("role after rejected owner-grant = %q, want viewer", role)
	}
}

// TestUpdateMemberRole_OwnersRoleProtected pins that the OWNER's membership
// cannot be re-roled by anyone through this endpoint — the only demotion
// path would be an ownership transfer, which does not exist here. There is
// no separate last-owner rule in the code; this absolute owner protection is
// what makes one unnecessary (the sole owner row can never be demoted or
// removed).
func TestUpdateMemberRole_OwnersRoleProtected(t *testing.T) {
	withDB(t)
	owner := uuid.Must(uuid.NewV7())
	org := seedOrg(t, owner)
	admin := uuid.Must(uuid.NewV7())
	seedMember(t, org.ID, admin, models.RoleAdmin)

	w := doJSON(asUser(admin), http.MethodPatch,
		"/api/orgs/"+org.ID.String()+"/members/"+owner.String(), `{"role":"viewer"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403 (body %s)", w.Code, w.Body.String())
	}
	if role, _ := roleOf(t, org.ID, owner); role != models.RoleOwner {
		t.Errorf("owner role after denied change = %q, want owner", role)
	}
}

// TestRemoveMember_OwnerCannotBeRemoved pins the removal half of owner
// protection: even an admin's DELETE leaves the owner row in place.
func TestRemoveMember_OwnerCannotBeRemoved(t *testing.T) {
	withDB(t)
	owner := uuid.Must(uuid.NewV7())
	org := seedOrg(t, owner)
	admin := uuid.Must(uuid.NewV7())
	seedMember(t, org.ID, admin, models.RoleAdmin)

	w := doJSON(asUser(admin), http.MethodDelete,
		"/api/orgs/"+org.ID.String()+"/members/"+owner.String(), "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403 (body %s)", w.Code, w.Body.String())
	}
	if _, exists := roleOf(t, org.ID, owner); !exists {
		t.Error("owner membership row was deleted despite the 403")
	}
}

// TestRemoveMember_CallerRoleMatrix: admins may remove non-owner members
// (row really deleted); editors may not (row really kept).
func TestRemoveMember_CallerRoleMatrix(t *testing.T) {
	withDB(t)
	owner := uuid.Must(uuid.NewV7())
	org := seedOrg(t, owner)
	admin := uuid.Must(uuid.NewV7())
	editor := uuid.Must(uuid.NewV7())
	target := uuid.Must(uuid.NewV7())
	seedMember(t, org.ID, admin, models.RoleAdmin)
	seedMember(t, org.ID, editor, models.RoleEditor)
	seedMember(t, org.ID, target, models.RoleViewer)

	// Editor first: the denial must leave the row for the admin case to remove.
	w := doJSON(asUser(editor), http.MethodDelete,
		"/api/orgs/"+org.ID.String()+"/members/"+target.String(), "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("editor remove: code = %d, want 403 (body %s)", w.Code, w.Body.String())
	}
	if _, exists := roleOf(t, org.ID, target); !exists {
		t.Fatal("denied removal still deleted the membership row")
	}

	w = doJSON(asUser(admin), http.MethodDelete,
		"/api/orgs/"+org.ID.String()+"/members/"+target.String(), "")
	if w.Code != http.StatusOK {
		t.Fatalf("admin remove: code = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	if _, exists := roleOf(t, org.ID, target); exists {
		t.Error("membership row survived the 200 removal")
	}
}

// TestNonMemberSeesNotFoundNotForbidden pins the disclosure contract: to a
// non-member, an existing organization is indistinguishable from a missing
// one — every org-scoped route answers 404 NOT_FOUND, never 403, so probing
// ids cannot confirm an org exists.
func TestNonMemberSeesNotFoundNotForbidden(t *testing.T) {
	withDB(t)
	owner := uuid.Must(uuid.NewV7())
	org := seedOrg(t, owner)
	stranger := uuid.Must(uuid.NewV7())

	cases := []struct {
		name, method, path, body string
	}{
		{"get org", http.MethodGet, "/api/orgs/" + org.ID.String(), ""},
		{"list members", http.MethodGet, "/api/orgs/" + org.ID.String() + "/members", ""},
		{"add member", http.MethodPost, "/api/orgs/" + org.ID.String() + "/members",
			`{"userId":"` + stranger.String() + `","role":"editor"}`},
		{"update member role", http.MethodPatch,
			"/api/orgs/" + org.ID.String() + "/members/" + owner.String(), `{"role":"editor"}`},
		{"remove member", http.MethodDelete,
			"/api/orgs/" + org.ID.String() + "/members/" + owner.String(), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doJSON(asUser(stranger), tc.method, tc.path, tc.body)
			if w.Code != http.StatusNotFound {
				t.Fatalf("code = %d, want 404 (body %s)", w.Code, w.Body.String())
			}
			if got := errorCode(t, w); got != "NOT_FOUND" {
				t.Errorf("error code = %q, want NOT_FOUND", got)
			}
		})
	}
	// The denied add must also not have written anything.
	if _, exists := roleOf(t, org.ID, stranger); exists {
		t.Error("non-member add-member attempt wrote a membership row")
	}
}

// TestInternalOrgLookup exercises the mesh-only mirror end to end against
// real rows: shared secret + X-User-ID resolve to the same GetOrganization
// handler, returning the acting user's role for an existing org and 404 for
// a missing one. Composition mirrors routes.SetupRouter (env-read secret).
func TestInternalOrgLookup(t *testing.T) {
	withDB(t)
	const secret = "test-internal-secret"
	t.Setenv("INTERNAL_API_TOKEN", secret)

	r := gin.New()
	internal := r.Group("/internal/v1",
		authverify.InternalTokenGin(config.GetEnv("INTERNAL_API_TOKEN", "")),
		InternalIdentity(),
	)
	internal.GET("/orgs/:id", GetOrganization)

	owner := uuid.Must(uuid.NewV7())
	org := seedOrg(t, owner)

	get := func(orgID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/internal/v1/orgs/"+orgID, nil)
		req.Header.Set(authverify.InternalTokenHeader, secret)
		req.Header.Set("X-User-ID", owner.String())
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	w := get(org.ID.String())
	if w.Code != http.StatusOK {
		t.Fatalf("existing org: code = %d (body %s)", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			ID   uuid.UUID `json:"id"`
			Role string    `json:"role"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode internal response: %v\n%s", err, w.Body.String())
	}
	// The "role" field is the contract document-service's orgclient consumes.
	if resp.Data.ID != org.ID || resp.Data.Role != models.RoleOwner {
		t.Errorf("internal lookup = (%s, %q), want (%s, owner)", resp.Data.ID, resp.Data.Role, org.ID)
	}

	if w := get(uuid.Must(uuid.NewV7()).String()); w.Code != http.StatusNotFound {
		t.Errorf("missing org: code = %d, want 404 (body %s)", w.Code, w.Body.String())
	}
}

// Route-registration pins for document-service. Two properties are guarded:
// every documented route pattern is actually registered, and every /api route
// sits behind RequireUser — a route added outside the guarded group would be
// reachable anonymously, which is exactly the drift this test exists to catch.
package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"fyredocs/shared/response"
)

// documentedRoutes mirrors the route table in
// docs/developer/services/document-service.md. Adding a route to
// SetupRouter without documenting it here fails TestNoUndocumentedRoutes.
var documentedRoutes = []struct {
	method, pattern string
}{
	{http.MethodGet, "/healthz"},
	{http.MethodGet, "/readyz"},
	{http.MethodGet, "/api/documents"},
	{http.MethodPost, "/api/documents"},
	{http.MethodGet, "/api/documents/:id"},
	{http.MethodGet, "/api/documents/:id/download"},
	{http.MethodPatch, "/api/documents/:id"},
	{http.MethodDelete, "/api/documents/:id"},
	{http.MethodPost, "/api/documents/:id/restore"},
	{http.MethodDelete, "/api/documents/:id/permanent"},
	{http.MethodPost, "/api/documents/:id/tags"},
	{http.MethodDelete, "/api/documents/:id/tags/:tagId"},
	{http.MethodPost, "/api/documents/workspace-hint"},
	{http.MethodGet, "/api/folders"},
	{http.MethodPost, "/api/folders"},
	{http.MethodPatch, "/api/folders/:id"},
	{http.MethodDelete, "/api/folders/:id"},
	{http.MethodGet, "/api/tags"},
	{http.MethodPost, "/api/tags"},
	{http.MethodDelete, "/api/tags/:id"},
	{http.MethodGet, "/api/exports"},
	{http.MethodPost, "/api/exports"},
	{http.MethodGet, "/api/exports/:id"},
	{http.MethodGet, "/api/exports/:id/download"},
}

func newRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetupRouter(r)
	return r
}

// TestEveryDocumentedRouteRegistered asserts the documented table and the
// live router agree, in both directions.
func TestEveryDocumentedRouteRegistered(t *testing.T) {
	registered := map[string]bool{}
	for _, ri := range newRouter().Routes() {
		registered[ri.Method+" "+ri.Path] = true
	}
	for _, want := range documentedRoutes {
		if !registered[want.method+" "+want.pattern] {
			t.Errorf("documented route not registered: %s %s", want.method, want.pattern)
		}
	}
}

// TestNoUndocumentedRoutes is the drift guard in the other direction: a route
// registered but absent from the documented table means the service docs
// (and this test's authorization sweep) no longer cover the real surface.
func TestNoUndocumentedRoutes(t *testing.T) {
	documented := map[string]bool{}
	for _, d := range documentedRoutes {
		documented[d.method+" "+d.pattern] = true
	}
	for _, ri := range newRouter().Routes() {
		if !documented[ri.Method+" "+ri.Path] {
			t.Errorf("undocumented route registered: %s %s (add it to documentedRoutes AND the service doc)", ri.Method, ri.Path)
		}
	}
}

// TestAPIRoutesRequireAuth sends a credential-less request to every /api
// route and demands the 401 envelope. RequireUser aborts before any handler
// runs, which is also what keeps this offline-safe: models.DB is nil here, so
// a route that escaped the auth group would panic instead of 401ing.
//
// /readyz is intentionally not requested: it is unauthenticated by design and
// pings the (nil) database; its registration is asserted above and its
// behavior belongs to the integration tier.
func TestAPIRoutesRequireAuth(t *testing.T) {
	r := newRouter()
	for _, tt := range documentedRoutes {
		if !strings.HasPrefix(tt.pattern, "/api/") {
			continue
		}
		t.Run(tt.method+" "+tt.pattern, func(t *testing.T) {
			// Params get syntactically plausible values; the request must die
			// at RequireUser regardless.
			target := strings.NewReplacer(
				":id", "0198c5b6-0000-7000-8000-000000000000",
				":tagId", "0198c5b6-0000-7000-8000-000000000001",
			).Replace(tt.pattern)
			req := httptest.NewRequest(tt.method, target, nil)
			// A forged identity header must not count as credentials (the
			// documents-service auth middleware only trusts verified JWTs).
			req.Header.Set("X-User-ID", "0198c5b6-0000-7000-8000-00000000dead")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("anonymous %s %s: status = %d, want 401", tt.method, target, w.Code)
			}
			var env response.APIResponse
			if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
				t.Fatalf("401 body is not the standard envelope: %v", err)
			}
			if env.Success || env.Error == nil || env.Error.Code != response.CodeUnauthorized {
				t.Errorf("401 envelope = %+v, want error.code %s", env, response.CodeUnauthorized)
			}
		})
	}
}

// TestHealthzIsPublic pins the one deliberately unauthenticated liveness
// route — container healthchecks carry no token.
func TestHealthzIsPublic(t *testing.T) {
	w := httptest.NewRecorder()
	newRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != http.StatusOK {
		t.Errorf("/healthz = %d, want 200", w.Code)
	}
}

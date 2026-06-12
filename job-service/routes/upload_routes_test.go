package routes

import (
	"testing"

	"github.com/gin-gonic/gin"
)

// routeSet builds the full router and returns "METHOD path" keys so tests
// can assert on the registered route table.
func routeSet(t *testing.T) map[string]bool {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetupRouter(r)
	set := map[string]bool{}
	for _, ri := range r.Routes() {
		set[ri.Method+" "+ri.Path] = true
	}
	return set
}

func TestSetupRouter_RegistersPresignedUploadRoutes(t *testing.T) {
	routes := routeSet(t)

	want := []string{
		"POST /api/uploads/init",
		"GET /api/uploads/:uploadId/parts",
		"POST /api/uploads/:uploadId/complete",
		"GET /api/uploads/:uploadId/status",
		"DELETE /api/uploads/:uploadId",
		// One-release 410 stub for the retired chunk protocol.
		"PUT /api/uploads/:uploadId/chunk",
	}
	for _, w := range want {
		if !routes[w] {
			t.Errorf("route %q must be registered", w)
		}
	}
}

func TestSetupRouter_RegistersToolGroups(t *testing.T) {
	routes := routeSet(t)

	for _, group := range []string{"convert-from-pdf", "convert-to-pdf", "organize-pdf", "optimize-pdf"} {
		for _, w := range []string{
			"POST /api/" + group + "/:tool",
			"GET /api/" + group + "/:tool",
			"GET /api/" + group + "/:tool/:id",
			"DELETE /api/" + group + "/:tool/:id",
			"GET /api/" + group + "/:tool/:id/download",
		} {
			if !routes[w] {
				t.Errorf("route %q must be registered", w)
			}
		}
	}
}

func TestSetupRouter_InfraRoutes(t *testing.T) {
	routes := routeSet(t)
	for _, w := range []string{"GET /healthz", "GET /readyz", "GET /api/jobs/history", "GET /api/jobs/:id/events"} {
		if !routes[w] {
			t.Errorf("route %q must be registered", w)
		}
	}
}

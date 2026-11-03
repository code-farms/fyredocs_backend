package routes

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"ai-service/handlers"
	"ai-service/internal/llm"
)

func TestSetupRouterRegistersRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetupRouter(r, &handlers.Ops{LLM: llm.Unconfigured{}})
	want := map[string]string{
		"/healthz":        http.MethodGet,
		"/readyz":         http.MethodGet,
		"/api/ai/ops/:op": http.MethodPost,
		"/api/ai/quota":   http.MethodGet,
	}
	got := map[string]string{}
	for _, ri := range r.Routes() {
		got[ri.Path] = ri.Method
	}
	for path, method := range want {
		if got[path] != method {
			t.Errorf("%s %s not registered", method, path)
		}
	}
	if len(got) != len(want) {
		t.Errorf("unexpected routes: %v", got)
	}
}

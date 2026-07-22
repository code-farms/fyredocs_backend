package response

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// GinRecovery must convert a handler panic into the standard 500 envelope with
// no stack trace leaked to the client body.
func TestGinRecoveryReturnsEnvelopeNoStackLeak(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(GinRecovery())
	r.GET("/boom", func(c *gin.Context) { panic("kaboom secret detail") })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var resp APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body is not the standard envelope: %v\n%s", err, rec.Body.String())
	}
	if resp.Success || resp.Error == nil || resp.Error.Code != CodeServerError {
		t.Errorf("unexpected envelope: %+v", resp)
	}
	if strings.Contains(rec.Body.String(), "kaboom") || strings.Contains(rec.Body.String(), ".go:") {
		t.Errorf("panic detail/stack leaked to client body: %s", rec.Body.String())
	}
}

// HTTPRecovery must do the same for the net/http gateway chain.
func TestHTTPRecoveryReturnsEnvelope(t *testing.T) {
	h := HTTPRecovery(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("kaboom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var resp APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body is not the standard envelope: %v", err)
	}
	if resp.Success || resp.Error == nil || resp.Error.Code != CodeServerError {
		t.Errorf("unexpected envelope: %+v", resp)
	}
}

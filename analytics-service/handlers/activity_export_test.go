package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAdminExportActivity_RejectsUnknownFormat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/admin/activity/export", AdminExportActivity)

	req := httptest.NewRequest(http.MethodGet, "/admin/activity/export?format=xlsx", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("format=xlsx: code = %d, want 400", w.Code)
	}
}

func TestExportContentTypes_CoversAcceptedFormats(t *testing.T) {
	// The handler's format check and this map must stay in lockstep: a format
	// accepted without a content type would stream with the wrong headers.
	for _, format := range []string{"csv", "json"} {
		if _, ok := exportContentTypes[format]; !ok {
			t.Errorf("accepted format %q has no content type", format)
		}
	}
	if len(exportContentTypes) != 2 {
		t.Errorf("exportContentTypes has %d entries, want 2 — update the handler's validation too", len(exportContentTypes))
	}
}

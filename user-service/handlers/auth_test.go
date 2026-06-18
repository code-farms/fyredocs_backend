package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"user-service/internal/models"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Acme Inc.":      "acme-inc",
		"  Hello World ": "hello-world",
		"@@@":            "org",
		"":               "org",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q)=%q want %q", in, got, want)
		}
	}
}

func TestRoleAtLeast(t *testing.T) {
	if !models.RoleAtLeast(models.RoleOwner, models.RoleAdmin) {
		t.Error("owner should satisfy admin")
	}
	if !models.RoleAtLeast(models.RoleAdmin, models.RoleAdmin) {
		t.Error("admin should satisfy admin")
	}
	if models.RoleAtLeast(models.RoleEditor, models.RoleAdmin) {
		t.Error("editor should NOT satisfy admin")
	}
	if models.RoleAtLeast("bogus", models.RoleViewer) {
		t.Error("unknown role should satisfy nothing")
	}
	if !models.ValidRole(models.RoleViewer) || models.ValidRole("nope") {
		t.Error("ValidRole mismatch")
	}
}

func TestRequireUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequireUser())
	r.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("no header: expected 401, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-User-ID", uuid.Must(uuid.NewV7()).String())
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("valid header: expected 200, got %d", w.Code)
	}
}

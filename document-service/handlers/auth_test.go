package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestParseQueryInt(t *testing.T) {
	cases := []struct {
		in       string
		fallback int
		want     int
	}{
		{"", 25, 25},
		{"10", 25, 10},
		{"abc", 25, 25},
		{"0", 25, 25},
		{"-5", 25, 25},
	}
	for _, c := range cases {
		if got := parseQueryInt(c.in, c.fallback); got != c.want {
			t.Errorf("parseQueryInt(%q,%d)=%d want %d", c.in, c.fallback, got, c.want)
		}
	}
}

func TestParseUUID(t *testing.T) {
	if v, ok := parseUUID(nil); !ok || v != nil {
		t.Error("nil pointer should be valid with nil value")
	}
	empty := "  "
	if v, ok := parseUUID(&empty); !ok || v != nil {
		t.Error("blank string should be valid with nil value")
	}
	valid := uuid.Must(uuid.NewV7()).String()
	if v, ok := parseUUID(&valid); !ok || v == nil {
		t.Error("valid uuid should parse")
	}
	bad := "not-a-uuid"
	if _, ok := parseUUID(&bad); ok {
		t.Error("invalid uuid should not be ok")
	}
}

func TestRequireUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequireUser())
	r.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	// Missing header → 401.
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("no X-User-ID: expected 401, got %d", w.Code)
	}

	// Valid header → 200.
	req = httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-User-ID", uuid.Must(uuid.NewV7()).String())
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("valid X-User-ID: expected 200, got %d", w.Code)
	}

	// Malformed header → 401.
	req = httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-User-ID", "garbage")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("malformed X-User-ID: expected 401, got %d", w.Code)
	}
}

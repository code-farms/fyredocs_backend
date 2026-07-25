package authverify

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func newInternalTokenRouter(expected string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/internal/ping", InternalTokenGin(expected), func(c *gin.Context) {
		c.String(http.StatusOK, "pong")
	})
	return r
}

func doInternal(r *gin.Engine, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/internal/ping", nil)
	if token != "" {
		req.Header.Set(InternalTokenHeader, token)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestInternalTokenGinUnconfiguredFailsClosed(t *testing.T) {
	r := newInternalTokenRouter("")
	for _, tok := range []string{"", "anything"} {
		if rec := doInternal(r, tok); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("token %q: expected 503 when unconfigured, got %d", tok, rec.Code)
		}
	}
}

func TestInternalTokenGinRejectsMissingAndWrong(t *testing.T) {
	r := newInternalTokenRouter("s3cret")
	cases := []string{"", "wrong", "s3cret ", "S3CRET", "s3cret-but-much-longer-than-expected"}
	for _, tok := range cases {
		if rec := doInternal(r, tok); rec.Code != http.StatusUnauthorized {
			t.Errorf("token %q: expected 401, got %d", tok, rec.Code)
		}
	}
}

func TestInternalTokenGinAcceptsCorrectToken(t *testing.T) {
	r := newInternalTokenRouter("s3cret")
	rec := doInternal(r, "s3cret")
	if rec.Code != http.StatusOK || rec.Body.String() != "pong" {
		t.Errorf("expected 200 pong, got %d %q", rec.Code, rec.Body.String())
	}
}

package response

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type bindTestReq struct {
	Email string `json:"email" binding:"required,email,max=254"`
	Name  string `json:"name" binding:"required,max=10"`
	Count int    `json:"count" binding:"omitempty,gt=0"`
}

func doBind(t *testing.T, body string) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	var req bindTestReq
	ok := BindJSON(c, &req)
	return rec, ok
}

func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) APIResponse {
	t.Helper()
	var resp APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not a valid envelope: %v", err)
	}
	return resp
}

func TestBindJSONSuccess(t *testing.T) {
	t.Parallel()
	rec, ok := doBind(t, `{"email":"a@b.com","name":"ok","count":3}`)
	if !ok {
		t.Fatalf("expected bind to succeed, got %s", rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("expected no response written on success, got %s", rec.Body.String())
	}
}

func TestBindJSONMalformedJSON(t *testing.T) {
	t.Parallel()
	rec, ok := doBind(t, `not-json`)
	if ok {
		t.Fatal("expected bind to fail")
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	resp := decodeEnvelope(t, rec)
	if resp.Error == nil || resp.Error.Code != CodeInvalidInput {
		t.Errorf("expected code %s, got %+v", CodeInvalidInput, resp.Error)
	}
}

func TestBindJSONValidationErrorsNameFields(t *testing.T) {
	t.Parallel()
	rec, ok := doBind(t, `{"email":"not-an-email","count":-2}`)
	if ok {
		t.Fatal("expected bind to fail")
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	resp := decodeEnvelope(t, rec)
	if resp.Error == nil || resp.Error.Code != CodeInvalidInput {
		t.Fatalf("expected code %s, got %+v", CodeInvalidInput, resp.Error)
	}
	details := resp.Error.Details
	for _, want := range []string{"email:", "name: is required", "count: must be greater than 0"} {
		if !strings.Contains(details, want) {
			t.Errorf("details %q missing %q", details, want)
		}
	}
}

func TestBindJSONOptionalFieldsPass(t *testing.T) {
	t.Parallel()
	// count is omitempty — omitting it entirely must not fail gt=0.
	_, ok := doBind(t, `{"email":"a@b.com","name":"ok"}`)
	if !ok {
		t.Fatal("expected bind to succeed with optional field omitted")
	}
}

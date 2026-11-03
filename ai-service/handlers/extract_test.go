package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"fyredocs/shared/authverify"
	"fyredocs/shared/queue"
	"fyredocs/shared/response"

	"ai-service/internal/extract"
	"ai-service/internal/llm"
)

func extractRouter(h *Ops, auth *authverify.AuthContext) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if auth != nil {
			authverify.SetGinAuth(c, *auth)
		}
		c.Next()
	})
	g := r.Group("/api/ai")
	g.Use(RequireUser())
	g.POST("/extract", h.Extract)
	return r
}

func postExtract(r http.Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/ai/extract", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

const idReply = "```json\n{\"docType\":\"id_document\",\"subtype\":\"pan\",\"title\":\"PAN card\",\"fields\":{\"fullName\":\"Asha Rao\",\"documentNumber\":\"ABCDE1234F\"}}\n```"

func jpeg(n int) string {
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(make([]byte, n))
}

func TestExtractReturnsTypedResultAndKeepsPIIOutOfAnalytics(t *testing.T) {
	var events []queue.AnalyticsEvent
	f := &fakeLLM{deltas: []string{idReply}}
	q := &fakeQuota{}
	h := &Ops{LLM: f, Quota: q, Publish: func(_ context.Context, e queue.AnalyticsEvent) error {
		events = append(events, e)
		return nil
	}}
	rec := postExtract(extractRouter(h, signedIn), `{"pages":[{"index":0,"text":"INCOME TAX DEPARTMENT"}],"images":["`+jpeg(100)+`"]}`)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data extract.Result `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.DocType != "id_document" || env.Data.Subtype != "pan" || env.Data.Fields["documentNumber"] != "ABCDE1234F" {
		t.Fatalf("%+v", env.Data)
	}
	if len(f.got.Messages[1].Images) != 1 || f.got.Tier != llm.TierFast {
		t.Fatalf("image not forwarded or wrong tier: %+v", f.got)
	}
	if len(q.reserved) != 1 || q.reserved[0] < llm.ImageTokens {
		t.Fatalf("reservation must cover the image: %v", q.reserved)
	}
	if len(events) != 1 || events[0].ToolType != "extract" || !strings.Contains(string(events[0].Metadata), `"docType":"id_document"`) {
		t.Fatalf("analytics %+v", events)
	}
	for _, pii := range []string{"ABCDE1234F", "Asha", "INCOME TAX"} {
		if strings.Contains(string(events[0].Metadata), pii) {
			t.Fatalf("analytics leaked %q", pii)
		}
	}
}

func TestExtractValidation(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{"nothing to read", `{}`, 400, response.CodeInvalidInput},
		{"three images", `{"images":["` + jpeg(1) + `","` + jpeg(1) + `","` + jpeg(1) + `"]}`, 413, response.CodeAIRequestTooLarge},
		{"not a data url", `{"images":["https://evil.example/x.jpg"]}`, 400, response.CodeInvalidInput},
		{"svg smuggling", `{"images":["data:image/svg+xml;base64,PHN2Zz4="]}`, 400, response.CodeInvalidInput},
		{"bad base64", `{"images":["data:image/jpeg;base64,@@@"]}`, 400, response.CodeInvalidInput},
		{"oversized image", `{"images":["` + jpeg(extractMaxImageSize+1) + `"]}`, 413, response.CodeAIRequestTooLarge},
		{"too many pages", `{"pages":[` + strings.TrimSuffix(strings.Repeat(`{"index":0,"text":"x"},`, 51), ",") + `]}`, 413, response.CodeAIRequestTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeLLM{deltas: []string{idReply}}
			rec := postExtract(extractRouter(&Ops{LLM: f}, signedIn), tc.body)
			if rec.Code != tc.status || errCode(t, rec) != tc.code {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
			if f.got.Messages != nil {
				t.Fatal("provider called for invalid input")
			}
		})
	}
}

func TestExtractGuestsAndProviderFailure(t *testing.T) {
	rec := postExtract(extractRouter(&Ops{LLM: &fakeLLM{}}, nil), `{"pages":[{"index":0,"text":"x"}]}`)
	if rec.Code != 401 {
		t.Fatalf("guest: %d", rec.Code)
	}
	rec = postExtract(extractRouter(&Ops{LLM: llm.Unconfigured{}}, signedIn), `{"pages":[{"index":0,"text":"x"}]}`)
	if rec.Code != 503 || errCode(t, rec) != response.CodeAIUnavailable {
		t.Fatalf("unconfigured: %d %s", rec.Code, rec.Body.String())
	}
}

func TestExtractUnparseableReplyDegradesNot500(t *testing.T) {
	rec := postExtract(extractRouter(&Ops{LLM: &fakeLLM{deltas: []string{"Sorry, I can't help."}}}, signedIn), `{"pages":[{"index":0,"text":"x"}]}`)
	var env struct {
		Data extract.Result `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); rec.Code != 200 || err != nil || env.Data.DocType != "other" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

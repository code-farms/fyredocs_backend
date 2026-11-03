package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"fyredocs/shared/authverify"
	"fyredocs/shared/response"

	"ai-service/internal/llm"
)

func transcribeRouter(h *Ops, auth *authverify.AuthContext) *gin.Engine {
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
	g.POST("/transcribe", h.Transcribe)
	return r
}

func postTranscribe(r http.Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/ai/transcribe", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestTranscribeSplitsPagesAndRequiresImages(t *testing.T) {
	f := &fakeLLM{deltas: []string{"<<page 1>>\nshopping list\n<<page 2>>\nmilk, eggs"}}
	q := &fakeQuota{}
	body := `{"images":["` + jpeg(50) + `","` + jpeg(50) + `"],"language":"eng+hin"}`
	rec := postTranscribe(transcribeRouter(&Ops{LLM: f, Quota: q}, signedIn), body)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data transcribeResult `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data.Pages) != 2 || env.Data.Pages[1] != "milk, eggs" {
		t.Fatalf("%+v", env.Data)
	}
	if !f.got.RequireImages || f.got.Tier != llm.TierSmart || len(f.got.Messages[1].Images) != 2 {
		t.Fatalf("request %+v", f.got)
	}
	if len(q.reserved) != 1 || q.reserved[0] < 2*llm.ImageTokens {
		t.Fatalf("reservation must cover both images: %v", q.reserved)
	}
}

func TestTranscribeValidation(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{"no images", `{"images":[]}`, 400, response.CodeInvalidInput},
		{"three images", `{"images":["` + jpeg(1) + `","` + jpeg(1) + `","` + jpeg(1) + `"]}`, 413, response.CodeAIRequestTooLarge},
		{"not a data url", `{"images":["https://x/y.jpg"]}`, 400, response.CodeInvalidInput},
		{"bad language", `{"images":["` + jpeg(1) + `"],"language":"deu"}`, 400, response.CodeInvalidInput},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeLLM{deltas: []string{"x"}}
			rec := postTranscribe(transcribeRouter(&Ops{LLM: f}, signedIn), tc.body)
			if rec.Code != tc.status || errCode(t, rec) != tc.code {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
			if f.got.Messages != nil {
				t.Fatal("provider called for invalid input")
			}
		})
	}
	if rec := postTranscribe(transcribeRouter(&Ops{LLM: &fakeLLM{}}, nil), `{"images":["`+jpeg(1)+`"]}`); rec.Code != 401 {
		t.Fatalf("guest: %d", rec.Code)
	}
}

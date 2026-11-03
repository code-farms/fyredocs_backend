package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"fyredocs/shared/authverify"
	"fyredocs/shared/queue"
	"fyredocs/shared/response"

	"ai-service/internal/doccache"
	"ai-service/internal/llm"
	"ai-service/internal/ops"
	"ai-service/internal/quota"
)

// fakeLLM streams fixed deltas, optionally failing before or after them.
type fakeLLM struct {
	deltas    []string
	err       error
	failAfter bool
	got       llm.Request
}

func (f *fakeLLM) Stream(_ context.Context, req llm.Request, onDelta func(string) error) (llm.Usage, error) {
	f.got = req
	if f.err != nil && !f.failAfter {
		return llm.Usage{}, f.err
	}
	for _, d := range f.deltas {
		if err := onDelta(d); err != nil {
			return llm.Usage{}, err
		}
	}
	if f.err != nil {
		return llm.Usage{}, f.err
	}
	return llm.Usage{InputTokens: 10, OutputTokens: len(f.deltas), CachedTokens: 4}, nil
}

func newRouter(h *Ops, auth *authverify.AuthContext) *gin.Engine {
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
	g.POST("/ops/:op", h.Run)
	return r
}

var signedIn = &authverify.AuthContext{UserID: "11111111-1111-1111-1111-111111111111", Plan: "free"}

func do(r http.Handler, op, body string, stream bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/ai/ops/"+op, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func frames(t *testing.T, body string) []Event {
	t.Helper()
	var out []Event
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env response.APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("not an envelope: %q", rec.Body.String())
	}
	if env.Error == nil {
		t.Fatalf("no error in envelope: %q", rec.Body.String())
	}
	return env.Error.Code
}

const summarizeBody = `{"pages":[{"index":0,"text":"hello"}]}`

func TestRunStreamsDeltasThenDone(t *testing.T) {
	var published []queue.AnalyticsEvent
	f := &fakeLLM{deltas: []string{"Hel", "lo"}}
	h := &Ops{LLM: f, MaxTokens: 99, Publish: func(_ context.Context, e queue.AnalyticsEvent) error {
		published = append(published, e)
		return nil
	}}
	rec := do(newRouter(h, signedIn), "summarize", summarizeBody, true)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("code=%d ct=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
	ev := frames(t, rec.Body.String())
	if len(ev) != 3 || ev[0].Text != "Hel" || ev[1].Text != "lo" || ev[2].Type != "done" || ev[2].Usage.CachedTokens != 4 {
		t.Fatalf("frames %+v", ev)
	}
	if f.got.Tier != llm.TierFast || f.got.MaxTokens != 99 || f.got.Messages[0].Role != "system" {
		t.Fatalf("request %+v", f.got)
	}
	if len(published) != 1 || published[0].EventType != "ai.op.completed" || published[0].ToolType != "summarize" || published[0].UserID != signedIn.UserID {
		t.Fatalf("analytics %+v", published)
	}
	if strings.Contains(string(published[0].Metadata), "hello") {
		t.Fatal("analytics must never carry document text")
	}
}

func TestRunJSONFallback(t *testing.T) {
	rec := do(newRouter(&Ops{LLM: &fakeLLM{deltas: []string{"a", "b"}}}, signedIn), "notes", summarizeBody, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d", rec.Code)
	}
	var env struct {
		Data opResult `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Data.Text != "ab" {
		t.Fatalf("body %q", rec.Body.String())
	}
}

func TestRunRejections(t *testing.T) {
	unavailable := &fakeLLM{err: &llm.Error{Code: response.CodeAIUnavailable, Message: "down"}}
	cases := []struct {
		name   string
		auth   *authverify.AuthContext
		llm    llm.Client
		op     string
		body   string
		stream bool
		status int
		code   string
	}{
		{"anonymous", nil, &fakeLLM{}, "summarize", summarizeBody, true, 401, response.CodeUnauthorized},
		{"guest", &authverify.AuthContext{IsGuest: true, UserID: "g"}, &fakeLLM{}, "summarize", summarizeBody, true, 401, response.CodeUnauthorized},
		{"unknown op", signedIn, &fakeLLM{}, "chat", summarizeBody, true, 404, response.CodeNotFound},
		{"bad json", signedIn, &fakeLLM{}, "summarize", `{`, true, 400, response.CodeInvalidInput},
		{"validation", signedIn, &fakeLLM{}, "ask", summarizeBody, true, 400, response.CodeInvalidInput},
		{"body over limit", signedIn, &fakeLLM{}, "summarize", `{"passage":"` + strings.Repeat("x", maxBodyBytes) + `"}`, true, 413, response.CodeAIRequestTooLarge},
		{"unconfigured before first byte", signedIn, llm.Unconfigured{}, "summarize", summarizeBody, true, 503, response.CodeAIUnavailable},
		{"provider down json", signedIn, unavailable, "summarize", summarizeBody, false, 503, response.CodeAIUnavailable},
		{"context too large", signedIn, &fakeLLM{err: &llm.Error{Code: response.CodeAIRequestTooLarge}}, "summarize", summarizeBody, true, 413, response.CodeAIRequestTooLarge},
		{"unclassified error is unavailable", signedIn, &fakeLLM{err: errors.New("dial tcp 10.0.0.1:443: secret")}, "summarize", summarizeBody, false, 503, response.CodeAIUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(newRouter(&Ops{LLM: tc.llm}, tc.auth), tc.op, tc.body, tc.stream)
			if rec.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			if got := errCode(t, rec); got != tc.code {
				t.Fatalf("code %q, want %q", got, tc.code)
			}
			if strings.Contains(rec.Body.String(), "10.0.0.1") {
				t.Fatal("provider error detail leaked to the client")
			}
		})
	}
}

func TestRunMidStreamFailureIsAnErrorFrame(t *testing.T) {
	f := &fakeLLM{deltas: []string{"partial"}, err: &llm.Error{Code: response.CodeAIUnavailable}, failAfter: true}
	rec := do(newRouter(&Ops{LLM: f}, signedIn), "summarize", summarizeBody, true)
	ev := frames(t, rec.Body.String())
	if rec.Code != 200 || len(ev) != 2 || ev[1].Type != "error" || ev[1].Code != response.CodeAIUnavailable {
		t.Fatalf("code=%d frames=%+v", rec.Code, ev)
	}
}

func TestRunRoutesAskToSmartTier(t *testing.T) {
	f := &fakeLLM{deltas: []string{"x"}}
	do(newRouter(&Ops{LLM: f}, signedIn), "ask", `{"pages":[{"index":0,"text":"t"}],"question":"why"}`, true)
	if f.got.Tier != llm.TierSmart {
		t.Fatalf("tier %q", f.got.Tier)
	}
}

// fakeQuota records reservations and settlements.
type fakeQuota struct {
	reserveErr error
	reserved   []int64
	settled    []llm.Usage
	statusErr  error
}

func (q *fakeQuota) Reserve(_ context.Context, userID, plan string, tokens int64) (quota.Reservation, error) {
	q.reserved = append(q.reserved, tokens)
	return quota.Reservation{Subject: quota.Subject(userID), Tokens: tokens}, q.reserveErr
}

func (q *fakeQuota) Settle(_ context.Context, _ quota.Reservation, u llm.Usage) error {
	q.settled = append(q.settled, u)
	return nil
}

func (q *fakeQuota) Status(_ context.Context, _, plan string) (quota.Status, error) {
	return quota.Status{Plan: plan, LimitTokens: 100, UsedTokens: 40, RemainingTokens: 60}, q.statusErr
}

func TestRunReservesWorstCaseThenSettlesActual(t *testing.T) {
	q := &fakeQuota{}
	rec := do(newRouter(&Ops{LLM: &fakeLLM{deltas: []string{"a"}}, Quota: q, MaxTokens: 100}, signedIn), "summarize", summarizeBody, true)
	if rec.Code != 200 || len(q.reserved) != 1 || len(q.settled) != 1 {
		t.Fatalf("code=%d reserved=%v settled=%v", rec.Code, q.reserved, q.settled)
	}
	if q.reserved[0] <= 100 {
		t.Fatalf("reservation %d must cover input estimate + output cap", q.reserved[0])
	}
	if q.settled[0] != (llm.Usage{InputTokens: 10, OutputTokens: 1, CachedTokens: 4}) {
		t.Fatalf("settled %+v", q.settled[0])
	}
}

func TestRunQuotaRefusals(t *testing.T) {
	f := &fakeLLM{deltas: []string{"a"}}
	rec := do(newRouter(&Ops{LLM: f, Quota: &fakeQuota{reserveErr: quota.ErrExceeded}}, signedIn), "summarize", summarizeBody, true)
	if rec.Code != http.StatusTooManyRequests || errCode(t, rec) != response.CodeAIQuotaExceeded {
		t.Fatalf("exceeded: %d %s", rec.Code, rec.Body.String())
	}
	if f.got.Messages != nil {
		t.Fatal("provider called despite exhausted quota")
	}
	rec = do(newRouter(&Ops{LLM: f, Quota: &fakeQuota{reserveErr: errors.New("db down")}}, signedIn), "summarize", summarizeBody, true)
	if rec.Code != http.StatusServiceUnavailable || errCode(t, rec) != response.CodeAIUnavailable {
		t.Fatalf("ledger failure must fail closed: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRunChargesPartialOutputOnFailure(t *testing.T) {
	q := &fakeQuota{}
	f := &fakeLLM{deltas: []string{"12345678"}, err: &llm.Error{Code: response.CodeAIUnavailable}, failAfter: true}
	do(newRouter(&Ops{LLM: f, Quota: q}, signedIn), "summarize", summarizeBody, true)
	if len(q.settled) != 1 || q.settled[0].OutputTokens != 2 || q.settled[0].InputTokens == 0 {
		t.Fatalf("partial output must be charged by estimate: %+v", q.settled)
	}

	q = &fakeQuota{}
	do(newRouter(&Ops{LLM: llm.Unconfigured{}, Quota: q}, signedIn), "summarize", summarizeBody, true)
	if len(q.settled) != 1 || q.settled[0] != (llm.Usage{}) {
		t.Fatalf("a failure before output must release the hold at zero cost: %+v", q.settled)
	}
}

func TestGetQuota(t *testing.T) {
	gin.SetMode(gin.TestMode)
	route := func(h *Ops, auth *authverify.AuthContext) *httptest.ResponseRecorder {
		r := newRouter(h, auth)
		r.GET("/api/ai/quota", func(c *gin.Context) {
			if auth != nil {
				authverify.SetGinAuth(c, *auth)
			}
			h.GetQuota(c)
		})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/ai/quota", nil))
		return rec
	}
	rec := route(&Ops{Quota: &fakeQuota{}}, signedIn)
	var env struct {
		Data quota.Status `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); rec.Code != 200 || err != nil || env.Data.RemainingTokens != 60 || env.Data.Plan != "free" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if rec := route(&Ops{Quota: &fakeQuota{statusErr: errors.New("x")}}, signedIn); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status failure: %d", rec.Code)
	}
}

// memDocs is an in-memory doccache.Store keyed like the Redis one.
type memDocs struct {
	m       map[string][]ops.Page
	failGet bool
	puts    int
}

func (d *memDocs) Put(_ context.Context, userID string, pages []ops.Page) (string, error) {
	ref, _, err := doccache.Ref(pages)
	if d.m == nil {
		d.m = map[string][]ops.Page{}
	}
	d.m[userID+":"+ref] = pages
	d.puts++
	return ref, err
}

func (d *memDocs) Get(_ context.Context, userID, ref string) ([]ops.Page, bool, error) {
	if d.failGet {
		return nil, false, errors.New("redis down")
	}
	p, ok := d.m[userID+":"+ref]
	return p, ok, nil
}

func TestDocRefFollowUpReusesCachedDocument(t *testing.T) {
	docs := &memDocs{}
	f := &fakeLLM{deltas: []string{"a"}}
	r := newRouter(&Ops{LLM: f, Docs: docs}, signedIn)

	first := frames(t, do(r, "summarize", summarizeBody, true).Body.String())
	ref := first[len(first)-1].DocRef
	if len(ref) != 64 {
		t.Fatalf("done frame must carry a docRef: %+v", first)
	}
	firstSystem := f.got.Messages[0]

	rec := do(r, "ask", `{"docRef":"`+ref+`","question":"why?"}`, true)
	ev := frames(t, rec.Body.String())
	if rec.Code != 200 || ev[len(ev)-1].DocRef != ref {
		t.Fatalf("follow-up: %d %s", rec.Code, rec.Body.String())
	}
	if f.got.Messages[0] != firstSystem {
		t.Fatal("a ref turn must rebuild a byte-identical document prefix")
	}
	if docs.puts != 1 {
		t.Fatalf("a ref turn must not rewrite the cache (puts=%d)", docs.puts)
	}
}

func TestDocRefMisses(t *testing.T) {
	ref := strings.Repeat("a", 64)
	other := &authverify.AuthContext{UserID: "22222222-2222-2222-2222-222222222222"}
	docs := &memDocs{}
	owner := newRouter(&Ops{LLM: &fakeLLM{deltas: []string{"x"}}, Docs: docs}, signedIn)
	got := frames(t, do(owner, "summarize", summarizeBody, true).Body.String())
	owned := got[len(got)-1].DocRef

	cases := []struct {
		name   string
		h      *Ops
		auth   *authverify.AuthContext
		ref    string
		status int
		code   string
	}{
		{"unknown ref", &Ops{LLM: &fakeLLM{}, Docs: docs}, signedIn, ref, 410, response.CodeAIDocumentExpired},
		{"another user's ref", &Ops{LLM: &fakeLLM{}, Docs: docs}, other, owned, 410, response.CodeAIDocumentExpired},
		{"cache down", &Ops{LLM: &fakeLLM{}, Docs: &memDocs{failGet: true}}, signedIn, ref, 410, response.CodeAIDocumentExpired},
		{"no cache configured", &Ops{LLM: &fakeLLM{}}, signedIn, ref, 410, response.CodeAIDocumentExpired},
		{"malformed ref", &Ops{LLM: &fakeLLM{}, Docs: docs}, signedIn, "ai:doc:*", 400, response.CodeInvalidInput},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(newRouter(tc.h, tc.auth), "summarize", `{"docRef":"`+tc.ref+`"}`, true)
			if rec.Code != tc.status || errCode(t, rec) != tc.code {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestPagesWinOverDocRefAndJSONCarriesRef(t *testing.T) {
	docs := &memDocs{}
	rec := do(newRouter(&Ops{LLM: &fakeLLM{deltas: []string{"a"}}, Docs: docs}, signedIn), "notes",
		`{"docRef":"`+strings.Repeat("b", 64)+`","pages":[{"index":0,"text":"hello"}]}`, false)
	var env struct {
		Data opResult `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); rec.Code != 200 || err != nil || len(env.Data.DocRef) != 64 || env.Data.DocRef == strings.Repeat("b", 64) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}
